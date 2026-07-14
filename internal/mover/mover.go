package mover

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nudgeworks/mysql-housekeeper/internal/config"
	mysqlutil "github.com/nudgeworks/mysql-housekeeper/internal/mysql"
	"github.com/nudgeworks/mysql-housekeeper/internal/state"
)

// Options controls a move run.
type Options struct {
	DryRun     bool
	TableFilter string
	Logger     *slog.Logger
	Now        func() time.Time
}

// TableResult summarizes one table's move.
type TableResult struct {
	Table     string `json:"table"`
	Estimated int64  `json:"estimated,omitempty"`
	Copied    int64  `json:"copied"`
	Deleted   int64  `json:"deleted"`
	DryRun    bool   `json:"dry_run"`
	Skipped   bool   `json:"skipped,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RunResult is the overall job stats.
type RunResult struct {
	RunID   int64         `json:"run_id"`
	DryRun  bool          `json:"dry_run"`
	Tables  []TableResult `json:"tables"`
	Elapsed string        `json:"elapsed"`
}

// Engine moves expired rows from primary to housekeeping.
type Engine struct {
	Primary      *sql.DB
	Housekeeping *sql.DB
	Cfg          *config.Config
	PrimarySchema string
	HouseSchema   string
}

// New creates an Engine after resolving default schemas.
func New(ctx context.Context, primary, house *sql.DB, cfg *config.Config) (*Engine, error) {
	ps, err := mysqlutil.CurrentSchema(ctx, primary)
	if err != nil {
		return nil, fmt.Errorf("primary schema: %w", err)
	}
	hs, err := mysqlutil.CurrentSchema(ctx, house)
	if err != nil {
		return nil, fmt.Errorf("housekeeping schema: %w", err)
	}
	return &Engine{
		Primary:       primary,
		Housekeeping:  house,
		Cfg:           cfg,
		PrimarySchema: ps,
		HouseSchema:   hs,
	}, nil
}

// Plan estimates expired row counts per table (no writes).
func (e *Engine) Plan(ctx context.Context, opts Options) ([]TableResult, error) {
	log := logger(opts)
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	tables, err := e.Cfg.FilterTables(opts.TableFilter)
	if err != nil {
		return nil, err
	}
	var out []TableResult
	for _, t := range tables {
		cutoff, err := config.Cutoff(t.Retention, nowFn())
		if err != nil {
			return nil, err
		}
		meta, err := mysqlutil.Introspect(ctx, e.Primary, e.PrimarySchema, t.Name)
		if err != nil {
			return nil, err
		}
		pk, err := mysqlutil.ResolvePK(meta, t.PrimaryKey)
		if err != nil {
			return nil, err
		}
		_ = pk
		if err := assertTimeColumn(meta, t.TimeColumn); err != nil {
			return nil, err
		}
		n, err := e.countExpired(ctx, t, cutoff)
		if err != nil {
			return nil, fmt.Errorf("plan %s: %w", t.Name, err)
		}
		log.Info("plan", "table", t.Name, "cutoff", cutoff.Format(time.RFC3339), "estimated", n)
		out = append(out, TableResult{Table: t.Name, Estimated: n, DryRun: true})
	}
	return out, nil
}

// Run moves expired rows for configured tables.
func (e *Engine) Run(ctx context.Context, opts Options) (*RunResult, error) {
	log := logger(opts)
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	start := nowFn()
	dryRun := opts.DryRun || e.Cfg.Defaults.DryRun

	if err := state.EnsureDDL(ctx, e.Housekeeping); err != nil {
		return nil, err
	}
	run, err := state.StartRun(ctx, e.Housekeeping, dryRun)
	if err != nil {
		return nil, fmt.Errorf("start run: %w", err)
	}

	tables, err := e.Cfg.FilterTables(opts.TableFilter)
	if err != nil {
		_ = state.FinishRun(ctx, e.Housekeeping, run.ID, "failed", err.Error(), nil)
		return nil, err
	}

	result := &RunResult{RunID: run.ID, DryRun: dryRun}
	var runErr error
	for _, t := range tables {
		tr, err := e.moveTable(ctx, run.ID, t, dryRun, nowFn, log)
		if err != nil {
			tr.Error = err.Error()
			result.Tables = append(result.Tables, tr)
			runErr = err
			log.Error("table failed", "table", t.Name, "err", err)
			break
		}
		result.Tables = append(result.Tables, tr)
	}

	status := "completed"
	errMsg := ""
	if runErr != nil {
		status = "failed"
		errMsg = runErr.Error()
	}
	result.Elapsed = nowFn().Sub(start).String()
	if err := state.FinishRun(ctx, e.Housekeeping, run.ID, status, errMsg, result); err != nil {
		log.Error("finish run", "err", err)
	}
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}

func (e *Engine) moveTable(ctx context.Context, runID int64, t config.TableCfg, dryRun bool, nowFn func() time.Time, log *slog.Logger) (TableResult, error) {
	tr := TableResult{Table: t.Name, DryRun: dryRun}
	cutoff, err := config.Cutoff(t.Retention, nowFn())
	if err != nil {
		return tr, err
	}

	var meta *mysqlutil.TableMeta
	if dryRun {
		meta, err = mysqlutil.Introspect(ctx, e.Primary, e.PrimarySchema, t.Name)
	} else {
		meta, err = mysqlutil.EnsureTable(ctx, e.Primary, e.Housekeeping, e.PrimarySchema, e.HouseSchema, t.Name)
	}
	if err != nil {
		return tr, err
	}
	pk, err := mysqlutil.ResolvePK(meta, t.PrimaryKey)
	if err != nil {
		return tr, err
	}
	if err := assertTimeColumn(meta, t.TimeColumn); err != nil {
		return tr, err
	}

	batchSize := e.Cfg.BatchSizeFor(t)
	maxRows := e.Cfg.MaxRowsFor(t)
	cols := meta.ColumnNames()

	var cursor []any
	cp, err := state.LoadCheckpoint(ctx, e.Housekeeping, t.Name, runID)
	if err != nil {
		return tr, err
	}
	if cp != nil && len(cp.LastPK) > 0 {
		cursor = cp.LastPK
		tr.Copied = cp.RowsMoved
		tr.Deleted = cp.RowsMoved
	}

	log.Info("move start",
		"table", t.Name,
		"cutoff", cutoff.Format(time.RFC3339),
		"batch_size", batchSize,
		"max_rows", maxRows,
		"dry_run", dryRun,
	)

	for tr.Copied < int64(maxRows) {
		limit := batchSize
		remaining := int64(maxRows) - tr.Copied
		if int64(limit) > remaining {
			limit = int(remaining)
		}

		batchStart := time.Now()
		rows, err := e.selectBatch(ctx, t, meta, pk, cols, cutoff, cursor, limit)
		if err != nil {
			return tr, err
		}
		if len(rows) == 0 {
			break
		}

		if dryRun {
			tr.Copied += int64(len(rows))
			tr.Deleted += int64(len(rows))
			cursor = pkValues(rows[len(rows)-1], pk, cols)
			log.Info("dry-run batch",
				"table", t.Name,
				"batch", len(rows),
				"total", tr.Copied,
				"duration_ms", time.Since(batchStart).Milliseconds(),
				"dry_run", true,
			)
		} else {
			if err := e.insertBatch(ctx, t.Name, cols, rows); err != nil {
				return tr, fmt.Errorf("insert: %w", err)
			}
			// Verify all PKs exist in housekeeping before delete.
			if err := e.verifyPresent(ctx, t.Name, pk, cols, rows); err != nil {
				return tr, fmt.Errorf("verify: %w", err)
			}
			deleted, err := e.deleteBatch(ctx, t.Name, pk, cols, rows)
			if err != nil {
				return tr, fmt.Errorf("delete: %w", err)
			}
			tr.Copied += int64(len(rows))
			tr.Deleted += deleted
			cursor = pkValues(rows[len(rows)-1], pk, cols)
			if err := state.SaveCheckpoint(ctx, e.Housekeeping, t.Name, runID, cursor, tr.Copied); err != nil {
				return tr, fmt.Errorf("checkpoint: %w", err)
			}
			log.Info("batch",
				"table", t.Name,
				"batch", len(rows),
				"copied", tr.Copied,
				"deleted", tr.Deleted,
				"duration_ms", time.Since(batchStart).Milliseconds(),
				"dry_run", false,
			)
		}

		if e.Cfg.Defaults.ThrottleMS > 0 {
			select {
			case <-ctx.Done():
				return tr, ctx.Err()
			case <-time.After(time.Duration(e.Cfg.Defaults.ThrottleMS) * time.Millisecond):
			}
		}
		if len(rows) < limit {
			break
		}
	}
	return tr, nil
}

func assertTimeColumn(meta *mysqlutil.TableMeta, timeCol string) error {
	for _, c := range meta.Columns {
		if c.Name == timeCol {
			return nil
		}
	}
	return fmt.Errorf("time_column %q not found on table %s", timeCol, meta.Name)
}

func (e *Engine) countExpired(ctx context.Context, t config.TableCfg, cutoff time.Time) (int64, error) {
	q := fmt.Sprintf("SELECT COUNT(*) FROM %s.%s WHERE %s < ?",
		mysqlutil.QuoteIdent(e.PrimarySchema),
		mysqlutil.QuoteIdent(t.Name),
		mysqlutil.QuoteIdent(t.TimeColumn),
	)
	args := []any{cutoff}
	if f := strings.TrimSpace(t.Filter); f != "" {
		q += " AND (" + f + ")"
	}
	var n int64
	err := e.Primary.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

func (e *Engine) selectBatch(
	ctx context.Context,
	t config.TableCfg,
	meta *mysqlutil.TableMeta,
	pk, cols []string,
	cutoff time.Time,
	cursor []any,
	limit int,
) ([][]any, error) {
	_ = meta
	colList := quoteList(cols)
	q := fmt.Sprintf("SELECT %s FROM %s.%s WHERE %s < ?",
		colList,
		mysqlutil.QuoteIdent(e.PrimarySchema),
		mysqlutil.QuoteIdent(t.Name),
		mysqlutil.QuoteIdent(t.TimeColumn),
	)
	args := []any{cutoff}
	if f := strings.TrimSpace(t.Filter); f != "" {
		q += " AND (" + f + ")"
	}
	if len(cursor) > 0 {
		tuple, err := buildPKGreater(pk, cursor)
		if err != nil {
			return nil, err
		}
		q += " AND " + tuple.Clause
		args = append(args, tuple.Args...)
	}
	q += fmt.Sprintf(" ORDER BY %s LIMIT %d", quoteList(pk), limit)

	rows, err := e.Primary.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		// Normalize []byte to string for stable JSON checkpoint / MySQL driver quirks.
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

type pkPredicate struct {
	Clause string
	Args   []any
}

// buildPKGreater builds (pk) > (cursor) for keyset pagination (lexicographic).
func buildPKGreater(pk []string, cursor []any) (pkPredicate, error) {
	if len(pk) != len(cursor) {
		return pkPredicate{}, fmt.Errorf("cursor length %d != pk length %d", len(cursor), len(pk))
	}
	if len(pk) == 1 {
		return pkPredicate{
			Clause: fmt.Sprintf("%s > ?", mysqlutil.QuoteIdent(pk[0])),
			Args:   []any{cursor[0]},
		}, nil
	}
	// (a > ?) OR (a = ? AND b > ?) OR (a = ? AND b = ? AND c > ?) ...
	var parts []string
	var args []any
	for i := range pk {
		var ands []string
		for j := 0; j < i; j++ {
			ands = append(ands, fmt.Sprintf("%s = ?", mysqlutil.QuoteIdent(pk[j])))
			args = append(args, cursor[j])
		}
		ands = append(ands, fmt.Sprintf("%s > ?", mysqlutil.QuoteIdent(pk[i])))
		args = append(args, cursor[i])
		parts = append(parts, "("+strings.Join(ands, " AND ")+")")
	}
	return pkPredicate{Clause: "(" + strings.Join(parts, " OR ") + ")", Args: args}, nil
}

func (e *Engine) insertBatch(ctx context.Context, table string, cols []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	placeholders := "(" + strings.TrimRight(strings.Repeat("?,", len(cols)), ",") + ")"
	var sb strings.Builder
	sb.WriteString("INSERT IGNORE INTO ")
	sb.WriteString(mysqlutil.QuoteIdent(e.HouseSchema))
	sb.WriteString(".")
	sb.WriteString(mysqlutil.QuoteIdent(table))
	sb.WriteString(" (")
	sb.WriteString(quoteList(cols))
	sb.WriteString(") VALUES ")
	args := make([]any, 0, len(rows)*len(cols))
	for i, row := range rows {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(placeholders)
		args = append(args, row...)
	}
	_, err := e.Housekeeping.ExecContext(ctx, sb.String(), args...)
	return err
}

func (e *Engine) verifyPresent(ctx context.Context, table string, pk, cols []string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	pkIdx := map[string]int{}
	for i, c := range cols {
		pkIdx[c] = i
	}
	for _, row := range rows {
		conds := make([]string, len(pk))
		args := make([]any, len(pk))
		for i, p := range pk {
			conds[i] = mysqlutil.QuoteIdent(p) + " = ?"
			args[i] = row[pkIdx[p]]
		}
		q := fmt.Sprintf("SELECT 1 FROM %s.%s WHERE %s LIMIT 1",
			mysqlutil.QuoteIdent(e.HouseSchema),
			mysqlutil.QuoteIdent(table),
			strings.Join(conds, " AND "),
		)
		var one int
		err := e.Housekeeping.QueryRowContext(ctx, q, args...).Scan(&one)
		if err == sql.ErrNoRows {
			pkVals, _ := json.Marshal(pkValues(row, pk, cols))
			return fmt.Errorf("row not found in housekeeping after insert pk=%s", string(pkVals))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) deleteBatch(ctx context.Context, table string, pk, cols []string, rows [][]any) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	pkIdx := map[string]int{}
	for i, c := range cols {
		pkIdx[c] = i
	}

	var total int64
	// Delete in chunks using OR of PK equality (portable for composite PK).
	const chunk = 100
	for i := 0; i < len(rows); i += chunk {
		end := i + chunk
		if end > len(rows) {
			end = len(rows)
		}
		part := rows[i:end]
		var ors []string
		var args []any
		for _, row := range part {
			ands := make([]string, len(pk))
			for j, p := range pk {
				ands[j] = mysqlutil.QuoteIdent(p) + " = ?"
				args = append(args, row[pkIdx[p]])
			}
			ors = append(ors, "("+strings.Join(ands, " AND ")+")")
		}
		q := fmt.Sprintf("DELETE FROM %s.%s WHERE %s",
			mysqlutil.QuoteIdent(e.PrimarySchema),
			mysqlutil.QuoteIdent(table),
			strings.Join(ors, " OR "),
		)
		res, err := e.Primary.ExecContext(ctx, q, args...)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func pkValues(row []any, pk, cols []string) []any {
	idx := map[string]int{}
	for i, c := range cols {
		idx[c] = i
	}
	out := make([]any, len(pk))
	for i, p := range pk {
		out[i] = row[idx[p]]
	}
	return out
}

func quoteList(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = mysqlutil.QuoteIdent(n)
	}
	return strings.Join(parts, ", ")
}

func logger(opts Options) *slog.Logger {
	if opts.Logger != nil {
		return opts.Logger
	}
	return slog.Default()
}

// BuildPKGreater is exported for unit tests.
func BuildPKGreater(pk []string, cursor []any) (string, []any, error) {
	p, err := buildPKGreater(pk, cursor)
	return p.Clause, p.Args, err
}
