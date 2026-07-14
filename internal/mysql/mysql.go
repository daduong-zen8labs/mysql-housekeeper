// Package mysqlutil provides MySQL 8+ helpers: connect, schema introspect, and ensure archive tables.
package mysqlutil

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Open connects and requires MySQL >= 8.0, sets UTC session timezone.
func Open(ctx context.Context, dsn string, maxExecTimeMS int) (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["parseTime"] = "true"
	cfg.Params["loc"] = "UTC"
	cfg.InterpolateParams = true

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	if err := requireMySQL8(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "SET time_zone = '+00:00'"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set time_zone: %w", err)
	}
	if maxExecTimeMS > 0 {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("SET SESSION max_execution_time = %d", maxExecTimeMS)); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set max_execution_time: %w", err)
		}
	}
	return db, nil
}

func requireMySQL8(ctx context.Context, db *sql.DB) error {
	var ver string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&ver); err != nil {
		return fmt.Errorf("VERSION(): %w", err)
	}
	major, minor, err := parseVersion(ver)
	if err != nil {
		return fmt.Errorf("parse VERSION %q: %w", ver, err)
	}
	if major < 8 {
		return fmt.Errorf("MySQL >= 8.0 required, got %s", ver)
	}
	_ = minor
	return nil
}

func parseVersion(ver string) (major, minor int, err error) {
	// e.g. "8.0.36", "8.4.0-log", "8.0.36-0ubuntu0.22.04.1"
	parts := strings.SplitN(ver, ".", 3)
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("unexpected version format")
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	minorStr := parts[1]
	if i := strings.IndexFunc(minorStr, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		minorStr = minorStr[:i]
	}
	minor, err = strconv.Atoi(minorStr)
	if err != nil {
		return 0, 0, err
	}
	return major, minor, nil
}

// Column describes a table column.
type Column struct {
	Name       string
	ColumnType string // full COLUMN_TYPE from information_schema
	Nullable   bool
	Extra      string
}

// TableMeta holds introspected table metadata.
type TableMeta struct {
	Schema  string
	Name    string
	Columns []Column
	PK      []string
	CreateSQL string
}

// QuoteIdent backticks an identifier.
func QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// CurrentSchema returns DATABASE().
func CurrentSchema(ctx context.Context, db *sql.DB) (string, error) {
	var schema sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&schema); err != nil {
		return "", err
	}
	if !schema.Valid || schema.String == "" {
		return "", fmt.Errorf("no default database in DSN; set database name")
	}
	return schema.String, nil
}

// Introspect loads column and PK metadata for a table.
func Introspect(ctx context.Context, db *sql.DB, schema, table string) (*TableMeta, error) {
	meta := &TableMeta{Schema: schema, Name: table}

	rows, err := db.QueryContext(ctx, `
SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, EXTRA
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c Column
		var nullable string
		if err := rows.Scan(&c.Name, &c.ColumnType, &nullable, &c.Extra); err != nil {
			return nil, err
		}
		c.Nullable = nullable == "YES"
		meta.Columns = append(meta.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(meta.Columns) == 0 {
		return nil, fmt.Errorf("table %s.%s not found", schema, table)
	}

	pkRows, err := db.QueryContext(ctx, `
SELECT COLUMN_NAME
FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND CONSTRAINT_NAME = 'PRIMARY'
ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("primary key: %w", err)
	}
	defer pkRows.Close()
	for pkRows.Next() {
		var col string
		if err := pkRows.Scan(&col); err != nil {
			return nil, err
		}
		meta.PK = append(meta.PK, col)
	}
	if err := pkRows.Err(); err != nil {
		return nil, err
	}
	if len(meta.PK) == 0 {
		return nil, fmt.Errorf("table %s.%s has no PRIMARY KEY (required in v1)", schema, table)
	}

	var dummy string
	if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+QuoteIdent(schema)+"."+QuoteIdent(table)).
		Scan(&dummy, &meta.CreateSQL); err != nil {
		return nil, fmt.Errorf("SHOW CREATE TABLE: %w", err)
	}
	return meta, nil
}

// EnsureTable creates dest table LIKE source if missing, then verifies column/PK compatibility.
func EnsureTable(ctx context.Context, primary, house *sql.DB, primarySchema, houseSchema, table string) (*TableMeta, error) {
	src, err := Introspect(ctx, primary, primarySchema, table)
	if err != nil {
		return nil, fmt.Errorf("introspect primary: %w", err)
	}

	var exists int
	err = house.QueryRowContext(ctx, `
SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, houseSchema, table).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if exists == 0 {
		// CREATE TABLE LIKE cannot cross servers; recreate from SHOW CREATE TABLE.
		createSQL := rewriteCreateSQL(src.CreateSQL, houseSchema, table)
		if _, err := house.ExecContext(ctx, createSQL); err != nil {
			return nil, fmt.Errorf("create housekeeping table: %w", err)
		}
	}

	dst, err := Introspect(ctx, house, houseSchema, table)
	if err != nil {
		return nil, fmt.Errorf("introspect housekeeping: %w", err)
	}
	if err := compatible(src, dst); err != nil {
		return nil, fmt.Errorf("schema drift for %s: %w", table, err)
	}
	return src, nil
}

func rewriteCreateSQL(createSQL, schema, table string) string {
	// SHOW CREATE TABLE returns "CREATE TABLE `t` (...)"
	// Prefix with schema and strip AUTO_INCREMENT=N for a clean copy template.
	s := createSQL
	if i := strings.Index(strings.ToUpper(s), "CREATE TABLE"); i >= 0 {
		s = s[i:]
	}
	// Replace first table name with schema.table
	rest := s
	upper := strings.ToUpper(rest)
	idx := strings.Index(upper, "CREATE TABLE")
	after := rest[idx+len("CREATE TABLE"):]
	after = strings.TrimSpace(after)
	// skip optional IF NOT EXISTS
	if strings.HasPrefix(strings.ToUpper(after), "IF NOT EXISTS") {
		after = strings.TrimSpace(after[len("IF NOT EXISTS"):])
	}
	// skip quoted name
	if strings.HasPrefix(after, "`") {
		end := strings.Index(after[1:], "`")
		if end >= 0 {
			after = after[1+end+1:]
		}
	} else {
		parts := strings.Fields(after)
		if len(parts) > 0 {
			after = strings.TrimPrefix(after, parts[0])
		}
	}
	out := "CREATE TABLE " + QuoteIdent(schema) + "." + QuoteIdent(table) + after
	// Drop AUTO_INCREMENT=N table option noise (optional)
	if j := strings.Index(strings.ToUpper(out), "AUTO_INCREMENT="); j >= 0 {
		// find end of that token
		k := j
		for k < len(out) && out[k] != ' ' && out[k] != '\n' {
			k++
		}
		out = out[:j] + out[k:]
	}
	return out
}

func compatible(src, dst *TableMeta) error {
	if len(src.PK) != len(dst.PK) {
		return fmt.Errorf("PK length mismatch primary=%v housekeeping=%v", src.PK, dst.PK)
	}
	for i := range src.PK {
		if src.PK[i] != dst.PK[i] {
			return fmt.Errorf("PK mismatch primary=%v housekeeping=%v", src.PK, dst.PK)
		}
	}
	srcCols := map[string]Column{}
	for _, c := range src.Columns {
		srcCols[c.Name] = c
	}
	dstCols := map[string]Column{}
	for _, c := range dst.Columns {
		dstCols[c.Name] = c
	}
	for name, sc := range srcCols {
		dc, ok := dstCols[name]
		if !ok {
			return fmt.Errorf("housekeeping missing column %q", name)
		}
		if !strings.EqualFold(sc.ColumnType, dc.ColumnType) {
			return fmt.Errorf("column %q type mismatch primary=%s housekeeping=%s", name, sc.ColumnType, dc.ColumnType)
		}
	}
	return nil
}

// ColumnNames returns ordered column names.
func (m *TableMeta) ColumnNames() []string {
	names := make([]string, len(m.Columns))
	for i, c := range m.Columns {
		names[i] = c.Name
	}
	return names
}

// ResolvePK returns configured PK override or introspected PK.
func ResolvePK(meta *TableMeta, override []string) ([]string, error) {
	if len(override) == 0 {
		return meta.PK, nil
	}
	colset := map[string]struct{}{}
	for _, c := range meta.Columns {
		colset[c.Name] = struct{}{}
	}
	for _, p := range override {
		if _, ok := colset[p]; !ok {
			return nil, fmt.Errorf("primary_key column %q not in table", p)
		}
	}
	return override, nil
}
