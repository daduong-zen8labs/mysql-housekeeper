package mover

import (
	"strings"
	"testing"
)

func TestBuildPKGreaterSingle(t *testing.T) {
	clause, args, err := BuildPKGreater([]string{"id"}, []any{10})
	if err != nil {
		t.Fatal(err)
	}
	if clause != "`id` > ?" {
		t.Fatalf("clause=%q", clause)
	}
	if len(args) != 1 || args[0] != 10 {
		t.Fatalf("args=%v", args)
	}
}

func TestBuildPKGreaterComposite(t *testing.T) {
	clause, args, err := BuildPKGreater([]string{"a", "b"}, []any{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clause, "`a` > ?") || !strings.Contains(clause, "`b` > ?") {
		t.Fatalf("clause=%q", clause)
	}
	if len(args) != 3 {
		t.Fatalf("args len=%d want 3: %v", len(args), args)
	}
}

func TestBuildPKGreaterMismatch(t *testing.T) {
	if _, _, err := BuildPKGreater([]string{"id"}, []any{1, 2}); err == nil {
		t.Fatal("expected error")
	}
}
