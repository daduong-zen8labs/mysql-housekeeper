//go:build integration

package mover_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/daduong-zen8labs/mysql-housekeeper/internal/config"
	"github.com/daduong-zen8labs/mysql-housekeeper/internal/mover"
	mysqlutil "github.com/daduong-zen8labs/mysql-housekeeper/internal/mysql"
)

func TestIntegrationMoveAndIdempotentRerun(t *testing.T) {
	primary, house := openIntegrationDBs(t)
	defer primary.Close()
	defer house.Close()
	ctx := context.Background()
	resetDemoData(t, ctx, primary, house)

	cfg := demoConfig(t)
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

	res, err := engine.Run(ctx, mover.Options{Now: func() time.Time { return fixedNow }, RunKey: "it-move"})
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

	res2, err := engine.Run(ctx, mover.Options{Now: func() time.Time { return fixedNow }, RunKey: "it-move-rerun"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range res2.Tables {
		if tr.Deleted != 0 || tr.Copied != 0 {
			t.Fatalf("rerun should move 0 rows: %+v", tr)
		}
	}
}

func TestIntegrationResumeAfterCap(t *testing.T) {
	primary, house := openIntegrationDBs(t)
	defer primary.Close()
	defer house.Close()
	ctx := context.Background()
	resetDemoData(t, ctx, primary, house)

	cfg := demoConfig(t)
	cfg.Defaults.MaxRowsPerRun = 1
	cfg.Defaults.BatchSize = 1
	cfg.Tables = []config.TableCfg{{
		Name:       "notification_logs",
		TimeColumn: "created_at",
		Retention:  "90d",
		Filter:     "status IN ('sent','failed')",
	}}

	engine, err := mover.New(ctx, primary, house, cfg)
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	opts := mover.Options{Now: func() time.Time { return fixedNow }, RunKey: "it-resume"}

	res1, err := engine.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Tables[0].Deleted != 1 {
		t.Fatalf("capped run deleted=%d want 1: %+v", res1.Tables[0].Deleted, res1.Tables[0])
	}
	assertCount(t, ctx, primary, "SELECT COUNT(*) FROM notification_logs WHERE id IN (1,2)", 1)
	assertCount(t, ctx, house, "SELECT COUNT(*) FROM notification_logs", 1)

	opts.Resume = true
	res2, err := engine.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Tables[0].Resumed {
		t.Fatalf("expected resumed: %+v", res2.Tables[0])
	}
	if got := res2.Tables[0].Deleted - 1; got != 1 {
		t.Fatalf("resume deleted delta=%d want 1 (total deleted=%d): %+v", got, res2.Tables[0].Deleted, res2.Tables[0])
	}
	assertCount(t, ctx, primary, "SELECT COUNT(*) FROM notification_logs WHERE id IN (1,2)", 0)
	assertCount(t, ctx, house, "SELECT COUNT(*) FROM notification_logs", 2)
}

func TestIntegrationCopyMode(t *testing.T) {
	primary, house := openIntegrationDBs(t)
	defer primary.Close()
	defer house.Close()
	ctx := context.Background()
	resetDemoData(t, ctx, primary, house)

	cfg := demoConfig(t)
	cfg.Defaults.Mode = config.ModeCopy
	cfg.Tables = []config.TableCfg{{
		Name:       "notification_logs",
		TimeColumn: "created_at",
		Retention:  "90d",
		Filter:     "status IN ('sent','failed')",
	}}
	engine, err := mover.New(ctx, primary, house, cfg)
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	res, err := engine.Run(ctx, mover.Options{Now: func() time.Time { return fixedNow }, RunKey: "it-copy"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tables[0].Copied != 2 || res.Tables[0].Deleted != 0 {
		t.Fatalf("copy mode: %+v", res.Tables[0])
	}
	assertCount(t, ctx, primary, "SELECT COUNT(*) FROM notification_logs WHERE id IN (1,2)", 2)
	assertCount(t, ctx, house, "SELECT COUNT(*) FROM notification_logs", 2)
}

func TestIntegrationDeleteMode(t *testing.T) {
	primary, house := openIntegrationDBs(t)
	defer primary.Close()
	defer house.Close()
	ctx := context.Background()
	resetDemoData(t, ctx, primary, house)

	cfg := demoConfig(t)
	cfg.Defaults.Mode = config.ModeDelete
	cfg.Tables = []config.TableCfg{{
		Name:       "notification_logs",
		TimeColumn: "created_at",
		Retention:  "90d",
		Filter:     "status IN ('sent','failed')",
	}}
	engine, err := mover.New(ctx, primary, house, cfg)
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	res, err := engine.Run(ctx, mover.Options{Now: func() time.Time { return fixedNow }, RunKey: "it-delete"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tables[0].Copied != 0 || res.Tables[0].Deleted != 2 {
		t.Fatalf("delete mode: %+v", res.Tables[0])
	}
	assertCount(t, ctx, primary, "SELECT COUNT(*) FROM notification_logs WHERE id IN (1,2)", 0)
}

func TestIntegrationOnConflictFail(t *testing.T) {
	primary, house := openIntegrationDBs(t)
	defer primary.Close()
	defer house.Close()
	ctx := context.Background()
	resetDemoData(t, ctx, primary, house)

	// Pre-seed archive with same PK so INSERT (fail) errors.
	if _, err := house.ExecContext(ctx, `
CREATE TABLE notification_logs (
  id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  status VARCHAR(32) NOT NULL,
  body TEXT NOT NULL,
  created_at DATETIME(6) NOT NULL
) ENGINE=InnoDB`); err != nil {
		t.Fatal(err)
	}
	if _, err := house.ExecContext(ctx, `
INSERT INTO notification_logs (id, status, body, created_at) VALUES
  (1, 'sent', 'preexisting', '2025-01-01 00:00:00.000000')`); err != nil {
		t.Fatal(err)
	}

	cfg := demoConfig(t)
	cfg.Defaults.OnConflict = config.ConflictFail
	cfg.Tables = []config.TableCfg{{
		Name:       "notification_logs",
		TimeColumn: "created_at",
		Retention:  "90d",
		Filter:     "status IN ('sent','failed')",
	}}
	engine, err := mover.New(ctx, primary, house, cfg)
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	_, err = engine.Run(ctx, mover.Options{Now: func() time.Time { return fixedNow }, RunKey: "it-conflict"})
	if err == nil {
		t.Fatal("expected on_conflict=fail to error on duplicate PK")
	}
	assertCount(t, ctx, primary, "SELECT COUNT(*) FROM notification_logs WHERE id IN (1,2)", 2)
}

func TestIntegrationConcurrentRunLock(t *testing.T) {
	primary, house := openIntegrationDBs(t)
	defer primary.Close()
	defer house.Close()
	ctx := context.Background()
	resetDemoData(t, ctx, primary, house)

	cfg := demoConfig(t)
	cfg.Tables = []config.TableCfg{{
		Name:       "notification_logs",
		TimeColumn: "created_at",
		Retention:  "90d",
		Filter:     "status IN ('sent','failed')",
	}}
	engine, err := mover.New(ctx, primary, house, cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Hold the same named lock on a dedicated connection (mirrors engine behavior).
	lockConn, err := house.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConn.Close()
	var got int
	if err := lockConn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", "hk:it-lock").Scan(&got); err != nil || got != 1 {
		t.Fatalf("setup GET_LOCK: got=%d err=%v", got, err)
	}
	defer func() {
		_, _ = lockConn.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", "hk:it-lock")
	}()

	fixedNow := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	_, err = engine.Run(ctx, mover.Options{
		Now:    func() time.Time { return fixedNow },
		RunKey: "it-lock",
	})
	if err == nil {
		t.Fatal("expected concurrent run_key to fail GET_LOCK")
	}
}

func openIntegrationDBs(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
	primaryDSN := envOr(t, "PRIMARY_DSN", "housekeeper:housekeeper@tcp(127.0.0.1:13306)/app?parseTime=true&loc=UTC")
	houseDSN := envOr(t, "HOUSEKEEPING_DSN", "housekeeper:housekeeper@tcp(127.0.0.1:13307)/archive?parseTime=true&loc=UTC")

	ctx := context.Background()
	primary, err := mysqlutil.Open(ctx, primaryDSN, 0)
	if err != nil {
		t.Skipf("primary unavailable: %v", err)
	}
	house, err := mysqlutil.Open(ctx, houseDSN, 0)
	if err != nil {
		primary.Close()
		t.Skipf("housekeeping unavailable: %v", err)
	}
	return primary, house
}

func demoConfig(t *testing.T) *config.Config {
	t.Helper()
	primaryDSN := envOr(t, "PRIMARY_DSN", "housekeeper:housekeeper@tcp(127.0.0.1:13306)/app?parseTime=true&loc=UTC")
	houseDSN := envOr(t, "HOUSEKEEPING_DSN", "housekeeper:housekeeper@tcp(127.0.0.1:13307)/archive?parseTime=true&loc=UTC")
	return &config.Config{
		Primary:      config.Endpoint{DSN: primaryDSN},
		Housekeeping: config.Endpoint{DSN: houseDSN},
		Defaults: config.Defaults{
			BatchSize:     100,
			MaxRowsPerRun: 10000,
			Mode:          config.ModeMove,
			OnConflict:    config.ConflictIgnore,
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
