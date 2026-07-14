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
func EnsureDDL(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS hk_job_runs (
			id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
			started_at DATETIME(6) NOT NULL,
			finished_at DATETIME(6) NULL,
			status VARCHAR(32) NOT NULL,
			dry_run TINYINT(1) NOT NULL DEFAULT 0,
			error_message TEXT NULL,
			stats_json JSON NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS hk_checkpoints (
			table_name VARCHAR(64) NOT NULL,
			run_id BIGINT UNSIGNED NOT NULL,
			last_pk_json JSON NULL,
			rows_moved BIGINT UNSIGNED NOT NULL DEFAULT 0,
			updated_at DATETIME(6) NOT NULL,
			PRIMARY KEY (table_name, run_id),
			KEY idx_hk_checkpoints_table (table_name, updated_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("ensure state ddl: %w", err)
		}
	}
	return nil
}

// Run represents a job execution.
type Run struct {
	ID     int64
	DryRun bool
}

// StartRun inserts a new job run row.
func StartRun(ctx context.Context, db *sql.DB, dryRun bool) (*Run, error) {
	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO hk_job_runs (started_at, status, dry_run) VALUES (?, 'running', ?)`,
		now, boolToTiny(dryRun))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Run{ID: id, DryRun: dryRun}, nil
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

// SaveCheckpoint upserts checkpoint progress for observability within a run.
func SaveCheckpoint(ctx context.Context, db *sql.DB, table string, runID int64, lastPK []any, rowsMoved int64) error {
	pkJSON, err := json.Marshal(lastPK)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO hk_checkpoints (table_name, run_id, last_pk_json, rows_moved, updated_at)
VALUES (?, ?, CAST(? AS JSON), ?, ?)
ON DUPLICATE KEY UPDATE
  last_pk_json = VALUES(last_pk_json),
  rows_moved = VALUES(rows_moved),
  updated_at = VALUES(updated_at)`,
		table, runID, string(pkJSON), rowsMoved, time.Now().UTC())
	return err
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
