package state

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEnsureDDL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery("FROM INFORMATION_SCHEMA.COLUMNS").
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS hk_job_runs").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS hk_checkpoints").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER TABLE hk_job_runs").WillReturnResult(sqlmock.NewResult(0, 0))

	if err := EnsureDDL(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStartFinishRunAndCheckpoint(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO hk_job_runs").WillReturnResult(sqlmock.NewResult(42, 1))
	run, err := StartRun(ctx, db, true, "nightly")
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != 42 || !run.DryRun || run.RunKey != "nightly" {
		t.Fatalf("%+v", run)
	}

	mock.ExpectExec("UPDATE hk_job_runs").WithArgs(sqlmock.AnyArg(), "completed", sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := FinishRun(ctx, db, 42, "completed", "", nil); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("UPDATE hk_job_runs").WithArgs(sqlmock.AnyArg(), "failed", sqlmock.AnyArg(), sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := FinishRun(ctx, db, 42, "failed", "boom", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("INSERT INTO hk_checkpoints").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := SaveCheckpoint(ctx, db, "logs", "nightly", []any{int64(9)}, 100); err != nil {
		t.Fatal(err)
	}

	rows := sqlmock.NewRows([]string{"last_pk_json", "rows_moved"}).AddRow(`[9]`, 100)
	mock.ExpectQuery("SELECT last_pk_json, rows_moved FROM hk_checkpoints").
		WithArgs("logs", "nightly").WillReturnRows(rows)
	cp, err := LoadCheckpoint(ctx, db, "logs", "nightly")
	if err != nil || cp == nil || cp.RowsMoved != 100 {
		t.Fatalf("%+v %v", cp, err)
	}

	mock.ExpectQuery("SELECT last_pk_json, rows_moved FROM hk_checkpoints").
		WithArgs("logs", "missing").WillReturnError(sql.ErrNoRows)
	cp, err = LoadCheckpoint(ctx, db, "logs", "missing")
	if err != nil || cp != nil {
		t.Fatalf("%+v %v", cp, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSaveCheckpointRequiresRunKey(t *testing.T) {
	if err := SaveCheckpoint(context.Background(), nil, "t", "", nil, 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestBoolToTinyAndNullIfEmpty(t *testing.T) {
	if boolToTiny(true) != 1 || boolToTiny(false) != 0 {
		t.Fatal("boolToTiny")
	}
	if nullIfEmpty("").Valid {
		t.Fatal("empty should be invalid")
	}
	if !nullIfEmpty("x").Valid {
		t.Fatal("non-empty should be valid")
	}
}
