// Package state persists job runs and per-table checkpoints on the housekeeping database.
package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// EnsureDDL creates housekeeper state tables on the housekeeping database.
// Early 0.x installs that used run_id-only checkpoints are recreated (progress reset).
func EnsureDDL(ctx context.Context, db *sql.DB) error {
	if err := migrateCheckpointsIfNeeded(ctx, db); err != nil {
		return err
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS hk_job_runs (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			started_at DATETIME(6) NOT NULL,
			finished_at DATETIME(6) NULL,
			status VARCHAR(32) NOT NULL,
			dry_run TINYINT(1) NOT NULL DEFAULT 0,
			run_key VARCHAR(128) NULL,
			error_message TEXT NULL,
			stats_json JSON NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS hk_checkpoints (
			table_name VARCHAR(64) NOT NULL,
			run_key VARCHAR(128) NOT NULL,
			last_pk_json JSON NULL,
			rows_moved BIGINT UNSIGNED NOT NULL DEFAULT 0,
			updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (table_name, run_key),
			KEY idx_hk_checkpoints_table (table_name, updated_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("ensure state ddl: %w", err)
		}
	}
	// Best-effort add run_key to older hk_job_runs without the column.
	_, _ = db.ExecContext(ctx, `ALTER TABLE hk_job_runs ADD COLUMN run_key VARCHAR(128) NULL`)
	return nil
}

func migrateCheckpointsIfNeeded(ctx context.Context, db *sql.DB) error {
	var n int
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'hk_checkpoints' AND COLUMN_NAME = 'run_id'`).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	// Old schema keyed by run_id — recreate for run_key-based resume.
	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS hk_checkpoints`); err != nil {
		return fmt.Errorf("migrate hk_checkpoints: %w", err)
	}
	return nil
}

// Run represents a job execution.
type Run struct {
	ID     int64
	DryRun bool
	RunKey string
}

// StartRun inserts a new job run row.
func StartRun(ctx context.Context, db *sql.DB, dryRun bool, runKey string) (*Run, error) {
	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO hk_job_runs (started_at, status, dry_run, run_key) VALUES (?, 'running', ?, ?)`,
		now, boolToTiny(dryRun), nullIfEmpty(runKey))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Run{ID: id, DryRun: dryRun, RunKey: runKey}, nil
}

// FinishRun marks a run as completed or failed.
func FinishRun(ctx context.Context, db *sql.DB, runID int64, status string, errMsg string, stats any) error {
	now := time.Now().UTC()
	if stats == nil {
		_, err := db.ExecContext(ctx, `
UPDATE hk_job_runs
SET finished_at = ?, status = ?, error_message = ?, stats_json = NULL
WHERE id = ?`, now, status, nullIfEmpty(errMsg), runID)
		return err
	}
	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
UPDATE hk_job_runs
SET finished_at = ?, status = ?, error_message = ?, stats_json = CAST(? AS JSON)
WHERE id = ?`, now, status, nullIfEmpty(errMsg), string(statsJSON), runID)
	return err
}

// Checkpoint is per-table progress for a stable run_key.
type Checkpoint struct {
	LastPK    []any
	RowsMoved int64
}

// SaveCheckpoint upserts checkpoint progress keyed by table + run_key.
func SaveCheckpoint(ctx context.Context, db *sql.DB, table, runKey string, lastPK []any, rowsMoved int64) error {
	if runKey == "" {
		return fmt.Errorf("run_key is required to save checkpoint")
	}
	pkJSON, err := json.Marshal(lastPK)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO hk_checkpoints (table_name, run_key, last_pk_json, rows_moved, updated_at)
VALUES (?, ?, CAST(? AS JSON), ?, ?)
ON DUPLICATE KEY UPDATE
  last_pk_json = VALUES(last_pk_json),
  rows_moved = VALUES(rows_moved),
  updated_at = VALUES(updated_at)`,
		table, runKey, string(pkJSON), rowsMoved, time.Now().UTC())
	return err
}

// LoadCheckpoint loads checkpoint for table+run_key, or nil if none.
func LoadCheckpoint(ctx context.Context, db *sql.DB, table, runKey string) (*Checkpoint, error) {
	if runKey == "" {
		return nil, nil
	}
	var pkJSON sql.NullString
	var rowsMoved int64
	err := db.QueryRowContext(ctx, `
SELECT last_pk_json, rows_moved FROM hk_checkpoints
WHERE table_name = ? AND run_key = ?`, table, runKey).Scan(&pkJSON, &rowsMoved)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cp := &Checkpoint{RowsMoved: rowsMoved}
	if pkJSON.Valid && pkJSON.String != "" && pkJSON.String != "null" {
		if err := json.Unmarshal([]byte(pkJSON.String), &cp.LastPK); err != nil {
			return nil, fmt.Errorf("decode last_pk_json: %w", err)
		}
	}
	return cp, nil
}

func boolToTiny(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullIfEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
