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

	if err := prepareSession(ctx, db, maxExecTimeMS); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// prepareSession pings, requires MySQL 8+, and sets session options.
func prepareSession(ctx context.Context, db *sql.DB, maxExecTimeMS int) error {
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	if err := requireMySQL8(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "SET time_zone = '+00:00'"); err != nil {
		return fmt.Errorf("set time_zone: %w", err)
	}
	if maxExecTimeMS > 0 {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("SET SESSION max_execution_time = %d", maxExecTimeMS)); err != nil {
			return fmt.Errorf("set max_execution_time: %w", err)
		}
	}
	return nil
}

func requireMySQL8(ctx context.Context, db *sql.DB) error {
	var ver string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&ver); err != nil {
		return fmt.Errorf("VERSION(): %w", err)
	}
	major, err := parseMajorVersion(ver)
	if err != nil {
		return fmt.Errorf("parse VERSION %q: %w", ver, err)
	}
	if major < 8 {
		return fmt.Errorf("MySQL >= 8.0 required, got %s", ver)
	}
	return nil
}

func parseMajorVersion(ver string) (int, error) {
	parts := strings.SplitN(ver, ".", 2)
	if len(parts) < 1 || parts[0] == "" {
		return 0, fmt.Errorf("unexpected version format")
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	return major, nil
}

// Column describes a table column.
type Column struct {
	Name       string
	ColumnType string // full COLUMN_TYPE from information_schema
}

// TableMeta holds introspected table metadata.
type TableMeta struct {
	Schema    string
	Name      string
	Columns   []Column
	PK        []string
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
SELECT COLUMN_NAME, COLUMN_TYPE
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err() checked below
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.Name, &c.ColumnType); err != nil {
			return nil, err
		}
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
	defer pkRows.Close() //nolint:errcheck // pkRows.Err() checked below
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

// EnsureTable creates dest table from primary SHOW CREATE TABLE if missing, then verifies column/PK compatibility.
// sourceTable is read from primary; destTable is created/checked on housekeeping (may differ).
func EnsureTable(ctx context.Context, primary, house *sql.DB, primarySchema, houseSchema, sourceTable, destTable string) (*TableMeta, error) {
	src, err := Introspect(ctx, primary, primarySchema, sourceTable)
	if err != nil {
		return nil, fmt.Errorf("introspect primary: %w", err)
	}

	var exists int
	err = house.QueryRowContext(ctx, `
SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, houseSchema, destTable).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if exists == 0 {
		// CREATE TABLE LIKE cannot cross servers; recreate from SHOW CREATE TABLE.
		createSQL := rewriteCreateSQL(src.CreateSQL, houseSchema, destTable)
		if _, err := house.ExecContext(ctx, createSQL); err != nil {
			return nil, fmt.Errorf("create housekeeping table: %w", err)
		}
	}

	dst, err := Introspect(ctx, house, houseSchema, destTable)
	if err != nil {
		return nil, fmt.Errorf("introspect housekeeping: %w", err)
	}
	if err := compatible(src, dst); err != nil {
		return nil, fmt.Errorf("schema drift for %s -> %s: %w", sourceTable, destTable, err)
	}
	return src, nil
}

func rewriteCreateSQL(createSQL, schema, table string) string {
	s := createSQL
	if i := strings.Index(strings.ToUpper(s), "CREATE TABLE"); i >= 0 {
		s = s[i:]
	}
	after := strings.TrimSpace(s[len("CREATE TABLE"):])
	if strings.HasPrefix(after, "`") {
		if end := strings.Index(after[1:], "`"); end >= 0 {
			after = after[1+end+1:]
		}
	}
	out := "CREATE TABLE " + QuoteIdent(schema) + "." + QuoteIdent(table) + after
	if j := strings.Index(strings.ToUpper(out), "AUTO_INCREMENT="); j >= 0 {
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
