package state

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEnsureDDL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS hk_job_runs").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS hk_checkpoints").WillReturnResult(sqlmock.NewResult(0, 0))

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
	run, err := StartRun(ctx, db, true)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != 42 || !run.DryRun {
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
	if err := SaveCheckpoint(ctx, db, "logs", 42, []any{int64(9)}, 100); err != nil {
		t.Fatal(err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
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
