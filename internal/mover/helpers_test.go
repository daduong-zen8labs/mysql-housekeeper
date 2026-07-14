package mover

import (
	"testing"

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
