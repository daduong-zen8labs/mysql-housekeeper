//go:build integration

package mover_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/nudgeworks/mysql-housekeeper/internal/config"
	"github.com/nudgeworks/mysql-housekeeper/internal/mover"
	mysqlutil "github.com/nudgeworks/mysql-housekeeper/internal/mysql"
)

func TestIntegrationMoveAndIdempotentRerun(t *testing.T) {
	primaryDSN := envOr(t, "PRIMARY_DSN", "housekeeper:housekeeper@tcp(127.0.0.1:13306)/app?parseTime=true&loc=UTC")
	houseDSN := envOr(t, "HOUSEKEEPING_DSN", "housekeeper:housekeeper@tcp(127.0.0.1:13307)/archive?parseTime=true&loc=UTC")

	ctx := context.Background()
	primary, err := mysqlutil.Open(ctx, primaryDSN, 0)
	if err != nil {
		t.Skipf("primary unavailable: %v", err)
	}
	defer primary.Close()
	house, err := mysqlutil.Open(ctx, houseDSN, 0)
	if err != nil {
		t.Skipf("housekeeping unavailable: %v", err)
	}
	defer house.Close()

	resetDemoData(t, ctx, primary, house)

	cfg := &config.Config{
		Primary:      config.Endpoint{DSN: primaryDSN},
		Housekeeping: config.Endpoint{DSN: houseDSN},
		Defaults: config.Defaults{
			BatchSize:     100,
			MaxRowsPerRun: 10000,
		},
		Tables: []config.TableCfg{
			{
				Name:       "notification_logs",
				TimeColumn: "created_at",
				Retention:  "90d",
				Filter:     "status IN ('sent','failed')",
			},
			{
				Name:       "audit_events",
				TimeColumn: "event_at",
				Retention:  "180d",
			},
		},
	}

	engine, err := mover.New(ctx, primary, house, cfg)
	if err != nil {
		t.Fatal(err)
	}

	fixedNow := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

	plan, err := engine.Plan(ctx, mover.Options{Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	byTable := map[string]int64{}
	for _, p := range plan {
		byTable[p.Table] = p.Estimated
	}
	if byTable["notification_logs"] != 2 {
		t.Fatalf("notification_logs estimated=%d want 2", byTable["notification_logs"])
	}
	if byTable["audit_events"] != 1 {
		t.Fatalf("audit_events estimated=%d want 1", byTable["audit_events"])
	}

	res, err := engine.Run(ctx, mover.Options{Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tables[0].Deleted != 2 || res.Tables[1].Deleted != 1 {
		t.Fatalf("unexpected delete counts: %+v", res.Tables)
	}

	assertCount(t, ctx, primary, "SELECT COUNT(*) FROM notification_logs", 2) // pending + recent
	assertCount(t, ctx, primary, "SELECT COUNT(*) FROM notification_logs WHERE id IN (1,2)", 0)
	assertCount(t, ctx, house, "SELECT COUNT(*) FROM notification_logs", 2)
	assertCount(t, ctx, primary, "SELECT COUNT(*) FROM audit_events", 1)
	assertCount(t, ctx, house, "SELECT COUNT(*) FROM audit_events", 1)

	// Idempotent re-run: no more expired rows.
	res2, err := engine.Run(ctx, mover.Options{Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range res2.Tables {
		if tr.Deleted != 0 || tr.Copied != 0 {
			t.Fatalf("rerun should move 0 rows: %+v", tr)
		}
	}
}

func resetDemoData(t *testing.T, ctx context.Context, primary, house *sql.DB) {
	t.Helper()
	for _, db := range []*sql.DB{primary, house} {
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS notification_logs")
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS audit_events")
	}
	_, _ = house.ExecContext(ctx, "DROP TABLE IF EXISTS hk_checkpoints")
	_, _ = house.ExecContext(ctx, "DROP TABLE IF EXISTS hk_job_runs")

	stmts := []string{
		`CREATE TABLE notification_logs (
		  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
		  status VARCHAR(32) NOT NULL,
		  body TEXT NOT NULL,
		  created_at DATETIME(6) NOT NULL
		) ENGINE=InnoDB`,
		`CREATE TABLE audit_events (
		  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
		  event_type VARCHAR(64) NOT NULL,
		  payload JSON NULL,
		  event_at DATETIME(6) NOT NULL
		) ENGINE=InnoDB`,
		`INSERT INTO notification_logs (id, status, body, created_at) VALUES
		  (1, 'sent', 'old-1', '2025-01-01 00:00:00.000000'),
		  (2, 'failed', 'old-2', '2025-06-01 00:00:00.000000'),
		  (3, 'pending', 'old-pending-not-moved', '2025-01-01 00:00:00.000000'),
		  (4, 'sent', 'recent', '2026-07-01 00:00:00.000000')`,
		`INSERT INTO audit_events (id, event_type, payload, event_at) VALUES
		  (1, 'login', JSON_OBJECT('u', 1), '2025-01-01 00:00:00.000000'),
		  (2, 'logout', JSON_OBJECT('u', 1), '2026-07-01 00:00:00.000000')`,
	}
	for _, s := range stmts {
		if _, err := primary.ExecContext(ctx, s); err != nil {
			t.Fatalf("seed: %v\n%s", err, s)
		}
	}
}

func assertCount(t *testing.T, ctx context.Context, db *sql.DB, q string, want int64) {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Fatalf("%s => %d want %d", q, n, want)
	}
}

func envOr(t *testing.T, key, fallback string) string {
	t.Helper()
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
