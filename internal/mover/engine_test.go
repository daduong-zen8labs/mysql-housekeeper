package mover

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/daduong-zen8labs/mysql-housekeeper/internal/config"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewEngine(t *testing.T) {
	primary, pmock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	house, hmock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer house.Close()

	pmock.ExpectQuery("SELECT DATABASE\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"DATABASE()"}).AddRow("app"))
	hmock.ExpectQuery("SELECT DATABASE\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"DATABASE()"}).AddRow("archive"))

	e, err := New(context.Background(), primary, house, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if e.PrimarySchema != "app" || e.HouseSchema != "archive" {
		t.Fatalf("%+v", e)
	}
}

func expectTableIntrospect(mock sqlmock.Sqlmock, schema, table string) {
	cols := sqlmock.NewRows([]string{"COLUMN_NAME", "COLUMN_TYPE"}).
		AddRow("id", "bigint unsigned").
		AddRow("created_at", "datetime(6)").
		AddRow("status", "varchar(32)")
	mock.ExpectQuery("FROM INFORMATION_SCHEMA.COLUMNS").
		WithArgs(schema, table).
		WillReturnRows(cols)
	mock.ExpectQuery("FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE").
		WithArgs(schema, table).
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("id"))
	createSQL := "CREATE TABLE `" + table + "` (`id` bigint unsigned NOT NULL, PRIMARY KEY (`id`))"
	mock.ExpectQuery("SHOW CREATE TABLE").
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).AddRow(table, createSQL))
}

func TestPlan(t *testing.T) {
	primary, pmock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	house, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer house.Close()

	e := &Engine{
		Primary:       primary,
		Housekeeping:  house,
		PrimarySchema: "app",
		HouseSchema:   "archive",
		Cfg: &config.Config{
			Defaults: config.Defaults{BatchSize: 100, MaxRowsPerRun: 1000},
			Tables: []config.TableCfg{{
				Name: "logs", TimeColumn: "created_at", Retention: "90d",
			}},
		},
	}

	fixed := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	cutoff := fixed.Add(-90 * 24 * time.Hour)

	expectTableIntrospect(pmock, "app", "logs")
	pmock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM").
		WithArgs(cutoff).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(42))

	res, err := e.Plan(context.Background(), Options{
		Logger: discardLogger(),
		Now:    func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Estimated != 42 {
		t.Fatalf("%+v", res)
	}
	if err := pmock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMoveTableDryRun(t *testing.T) {
	primary, pmock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()

	e := &Engine{
		Primary:       primary,
		PrimarySchema: "app",
		HouseSchema:   "archive",
		Cfg: &config.Config{
			Defaults: config.Defaults{BatchSize: 10, MaxRowsPerRun: 100},
		},
	}

	fixed := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	cutoff := fixed.Add(-7 * 24 * time.Hour)
	tcfg := config.TableCfg{Name: "logs", TimeColumn: "created_at", Retention: "7d"}

	expectTableIntrospect(pmock, "app", "logs")

	pmock.ExpectQuery("SELECT `id`, `created_at`, `status` FROM").
		WithArgs(cutoff).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "status"}).
			AddRow(int64(1), fixed.Add(-10*24*time.Hour), "sent").
			AddRow(int64(2), fixed.Add(-8*24*time.Hour), "sent"))

	tr, err := e.moveTable(context.Background(), "demo", tcfg, true, func() time.Time { return fixed }, discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Copied != 2 || !tr.DryRun {
		t.Fatalf("%+v", tr)
	}
	if err := pmock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMoveTableRealBatch(t *testing.T) {
	primary, pmock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	house, hmock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer house.Close()

	e := &Engine{
		Primary:       primary,
		Housekeeping:  house,
		PrimarySchema: "app",
		HouseSchema:   "archive",
		Cfg: &config.Config{
			Defaults: config.Defaults{BatchSize: 10, MaxRowsPerRun: 100},
		},
	}

	fixed := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	cutoff := fixed.Add(-7 * 24 * time.Hour)
	tcfg := config.TableCfg{Name: "logs", TimeColumn: "created_at", Retention: "7d"}

	// EnsureTable: introspect primary, table exists on house, introspect house
	expectTableIntrospect(pmock, "app", "logs")
	hmock.ExpectQuery("FROM INFORMATION_SCHEMA.TABLES").
		WithArgs("archive", "logs").
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(1))
	expectTableIntrospect(hmock, "archive", "logs")

	pmock.ExpectQuery("SELECT `id`, `created_at`, `status` FROM").
		WithArgs(cutoff).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "status"}).
			AddRow(int64(1), fixed.Add(-10*24*time.Hour), "sent"))

	hmock.ExpectExec("INSERT IGNORE INTO").WillReturnResult(sqlmock.NewResult(0, 1))
	hmock.ExpectQuery("SELECT 1 FROM").WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	pmock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 1))
	hmock.ExpectExec("INSERT INTO hk_checkpoints").WillReturnResult(sqlmock.NewResult(0, 1))

	tr, err := e.moveTable(context.Background(), "demo", tcfg, false, func() time.Time { return fixed }, discardLogger(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Copied != 1 || tr.Deleted != 1 {
		t.Fatalf("%+v", tr)
	}
	if err := pmock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := hmock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
