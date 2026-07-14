package mysqlutil

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestOpenInvalidDSN(t *testing.T) {
	_, err := Open(context.Background(), "://bad", 0)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestPrepareSessionOK(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectPing()
	mock.ExpectQuery("SELECT VERSION\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.36"))
	mock.ExpectExec("SET time_zone = '\\+00:00'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET SESSION max_execution_time = 1000").WillReturnResult(sqlmock.NewResult(0, 0))

	if err := prepareSession(context.Background(), db, 1000); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareSessionRejectsMySQL5(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectPing()
	mock.ExpectQuery("SELECT VERSION\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("5.7.42"))

	if err := prepareSession(context.Background(), db, 0); err == nil {
		t.Fatal("expected version error")
	}
}

func TestRequireMySQL8(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT VERSION\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.4.0-log"))
	if err := requireMySQL8(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentSchema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT DATABASE\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"DATABASE()"}).AddRow("app"))
	got, err := CurrentSchema(context.Background(), db)
	if err != nil || got != "app" {
		t.Fatalf("%q %v", got, err)
	}

	mock.ExpectQuery("SELECT DATABASE\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"DATABASE()"}).AddRow(nil))
	if _, err := CurrentSchema(context.Background(), db); err == nil {
		t.Fatal("expected empty database error")
	}
}

func expectIntrospect(mock sqlmock.Sqlmock, schema, table, createSQL string) {
	cols := sqlmock.NewRows([]string{"COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE", "EXTRA"}).
		AddRow("id", "bigint unsigned", "NO", "auto_increment").
		AddRow("created_at", "datetime(6)", "NO", "")
	mock.ExpectQuery("FROM INFORMATION_SCHEMA.COLUMNS").
		WithArgs(schema, table).
		WillReturnRows(cols)
	mock.ExpectQuery("FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE").
		WithArgs(schema, table).
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("id"))
	mock.ExpectQuery("SHOW CREATE TABLE").
		WillReturnRows(sqlmock.NewRows([]string{"Table", "Create Table"}).AddRow(table, createSQL))
}

func TestIntrospect(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	createSQL := "CREATE TABLE `logs` (\n  `id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB"
	expectIntrospect(mock, "app", "logs", createSQL)

	meta, err := Introspect(context.Background(), db, "app", "logs")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "logs" || len(meta.PK) != 1 || meta.PK[0] != "id" || len(meta.Columns) != 2 {
		t.Fatalf("%+v", meta)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestIntrospectNoPK(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery("FROM INFORMATION_SCHEMA.COLUMNS").
		WithArgs("app", "heap").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE", "EXTRA"}).
			AddRow("body", "text", "YES", ""))
	mock.ExpectQuery("FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE").
		WithArgs("app", "heap").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}))

	if _, err := Introspect(context.Background(), db, "app", "heap"); err == nil {
		t.Fatal("expected no PK error")
	}
}

func TestEnsureTableCreatesWhenMissing(t *testing.T) {
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

	createSQL := "CREATE TABLE `logs` (\n  `id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `created_at` datetime(6) NOT NULL,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB AUTO_INCREMENT=5"
	expectIntrospect(pmock, "app", "logs", createSQL)

	hmock.ExpectQuery("FROM INFORMATION_SCHEMA.TABLES").
		WithArgs("archive", "logs").
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(0))
	hmock.ExpectExec("CREATE TABLE `archive`.`logs`").WillReturnResult(sqlmock.NewResult(0, 0))
	expectIntrospect(hmock, "archive", "logs", createSQL)

	meta, err := EnsureTable(context.Background(), primary, house, "app", "archive", "logs")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "logs" {
		t.Fatalf("%+v", meta)
	}
	if err := pmock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := hmock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureTableExistingCompatible(t *testing.T) {
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

	createSQL := "CREATE TABLE `logs` (`id` bigint unsigned NOT NULL, PRIMARY KEY (`id`))"
	expectIntrospect(pmock, "app", "logs", createSQL)
	hmock.ExpectQuery("FROM INFORMATION_SCHEMA.TABLES").
		WithArgs("archive", "logs").
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(1))
	expectIntrospect(hmock, "archive", "logs", createSQL)

	if _, err := EnsureTable(context.Background(), primary, house, "app", "archive", "logs"); err != nil {
		t.Fatal(err)
	}
}
