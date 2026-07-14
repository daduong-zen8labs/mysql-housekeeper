package mover

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/daduong-zen8labs/mysql-housekeeper/internal/config"
	mysqlutil "github.com/daduong-zen8labs/mysql-housekeeper/internal/mysql"
)

func TestAssertTimeColumn(t *testing.T) {
	meta := &mysqlutil.TableMeta{
		Name:    "logs",
		Columns: []mysqlutil.Column{{Name: "id"}, {Name: "created_at"}},
	}
	if err := assertTimeColumn(meta, "created_at"); err != nil {
		t.Fatal(err)
	}
	if err := assertTimeColumn(meta, "missing"); err == nil {
		t.Fatal("expected error")
	}
}

func TestQuoteListAndPKValues(t *testing.T) {
	if quoteList([]string{"id", "tenant"}) != "`id`, `tenant`" {
		t.Fatal(quoteList([]string{"id", "tenant"}))
	}
	cols := []string{"id", "body", "tenant"}
	row := []any{int64(1), "x", int64(9)}
	got := pkValues(row, []string{"tenant", "id"}, cols)
	if len(got) != 2 || got[0] != int64(9) || got[1] != int64(1) {
		t.Fatalf("%v", got)
	}
}

func TestFilterTables(t *testing.T) {
	cfg := &config.Config{
		Tables: []config.TableCfg{
			{Name: "a"},
			{Name: "b"},
		},
	}
	all, err := cfg.FilterTables("")
	if err != nil || len(all) != 2 {
		t.Fatalf("%v %v", all, err)
	}
	one, err := cfg.FilterTables("a")
	if err != nil || len(one) != 1 || one[0].Name != "a" {
		t.Fatalf("%v %v", one, err)
	}
	if _, err := cfg.FilterTables("nope"); err == nil {
		t.Fatal("expected missing table error")
	}
}

func TestInsertVerifyDeleteBatch(t *testing.T) {
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
		Cfg:           &config.Config{},
	}

	cols := []string{"id", "body"}
	rows := [][]any{
		{int64(1), "a"},
		{int64(2), "b"},
	}

	hmock.ExpectExec("INSERT IGNORE INTO").WillReturnResult(sqlmock.NewResult(0, 2))
	if err := e.insertBatch(context.Background(), "logs", cols, rows); err != nil {
		t.Fatal(err)
	}

	hmock.ExpectQuery("SELECT 1 FROM").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	hmock.ExpectQuery("SELECT 1 FROM").WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	if err := e.verifyPresent(context.Background(), "logs", []string{"id"}, cols, rows); err != nil {
		t.Fatal(err)
	}

	pmock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 2))
	n, err := e.deleteBatch(context.Background(), "logs", []string{"id"}, cols, rows)
	if err != nil || n != 2 {
		t.Fatalf("deleted=%d err=%v", n, err)
	}

	if err := pmock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if err := hmock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCountExpired(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := &Engine{Primary: db, PrimarySchema: "app", Cfg: &config.Config{}}
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM").
		WithArgs(cutoff).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(7))
	n, err := e.countExpired(context.Background(), config.TableCfg{
		Name: "logs", TimeColumn: "created_at", Filter: "status = 'sent'",
	}, cutoff)
	if err != nil || n != 7 {
		t.Fatalf("%d %v", n, err)
	}
}
