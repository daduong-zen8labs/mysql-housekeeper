package mover

import (
	"strings"
	"testing"
)

func TestBuildPKGreaterSingle(t *testing.T) {
	p, err := buildPKGreater([]string{"id"}, []any{10})
	if err != nil {
		t.Fatal(err)
	}
	if p.Clause != "`id` > ?" {
		t.Fatalf("clause=%q", p.Clause)
	}
	if len(p.Args) != 1 || p.Args[0] != 10 {
		t.Fatalf("args=%v", p.Args)
	}
}

func TestBuildPKGreaterComposite(t *testing.T) {
	p, err := buildPKGreater([]string{"a", "b"}, []any{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Clause, "`a` > ?") || !strings.Contains(p.Clause, "`b` > ?") {
		t.Fatalf("clause=%q", p.Clause)
	}
	if len(p.Args) != 3 {
		t.Fatalf("args len=%d want 3: %v", len(p.Args), p.Args)
	}
}

func TestBuildPKGreaterMismatch(t *testing.T) {
	if _, err := buildPKGreater([]string{"id"}, []any{1, 2}); err == nil {
		t.Fatal("expected error")
	}
}
