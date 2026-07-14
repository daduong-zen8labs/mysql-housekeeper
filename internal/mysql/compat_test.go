package mysqlutil

import (
	"testing"
)

func TestCompatible(t *testing.T) {
	src := &TableMeta{
		PK: []string{"id"},
		Columns: []Column{
			{Name: "id", ColumnType: "bigint unsigned"},
			{Name: "body", ColumnType: "text"},
		},
	}
	dst := &TableMeta{
		PK: []string{"id"},
		Columns: []Column{
			{Name: "id", ColumnType: "bigint unsigned"},
			{Name: "body", ColumnType: "text"},
		},
	}
	if err := compatible(src, dst); err != nil {
		t.Fatal(err)
	}

	dstBad := &TableMeta{
		PK: []string{"id"},
		Columns: []Column{
			{Name: "id", ColumnType: "int"},
		},
	}
	if err := compatible(src, dstBad); err == nil {
		t.Fatal("expected type mismatch")
	}
}

func TestResolvePK(t *testing.T) {
	meta := &TableMeta{
		PK: []string{"id"},
		Columns: []Column{
			{Name: "id", ColumnType: "bigint"},
			{Name: "tenant_id", ColumnType: "bigint"},
		},
	}
	pk, err := ResolvePK(meta, nil)
	if err != nil || len(pk) != 1 || pk[0] != "id" {
		t.Fatalf("%v %v", pk, err)
	}
	pk, err = ResolvePK(meta, []string{"tenant_id", "id"})
	if err != nil || len(pk) != 2 {
		t.Fatalf("%v %v", pk, err)
	}
	if _, err := ResolvePK(meta, []string{"missing"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestColumnNames(t *testing.T) {
	m := &TableMeta{Columns: []Column{{Name: "a"}, {Name: "b"}}}
	got := m.ColumnNames()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%v", got)
	}
}
