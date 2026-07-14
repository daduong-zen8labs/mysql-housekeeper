package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseRetention(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"90d", 90 * 24 * time.Hour},
		{"12h", 12 * time.Hour},
		{"30m", 30 * time.Minute},
		{"60s", 60 * time.Second},
		{"1d", 24 * time.Hour},
	}
	for _, tc := range cases {
		got, err := ParseRetention(tc.in)
		if err != nil {
			t.Fatalf("ParseRetention(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseRetention(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseRetentionInvalid(t *testing.T) {
	for _, in := range []string{"", "0d", "-1h", "abc", "10x"} {
		if _, err := ParseRetention(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	_ = os.Setenv("PRIMARY_DSN", "user:pass@tcp(localhost:3306)/primary")
	_ = os.Setenv("HOUSEKEEPING_DSN", "user:pass@tcp(localhost:3307)/hk")
	content := `
primary:
  dsn: "${PRIMARY_DSN}"
housekeeping:
  dsn: "${HOUSEKEEPING_DSN}"
defaults:
  batch_size: 500
tables:
  - name: logs
    time_column: created_at
    retention: 7d
    filter: "status = 'done'"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Primary.DSN != "user:pass@tcp(localhost:3306)/primary" {
		t.Fatalf("dsn expand failed: %q", cfg.Primary.DSN)
	}
	if cfg.Defaults.BatchSize != 500 {
		t.Fatalf("batch_size=%d", cfg.Defaults.BatchSize)
	}
	if len(cfg.Tables) != 1 || cfg.Tables[0].Name != "logs" {
		t.Fatalf("tables=%+v", cfg.Tables)
	}
}

func TestValidateRejectsBadFilter(t *testing.T) {
	cfg := &Config{
		Primary:      Endpoint{DSN: "a"},
		Housekeeping: Endpoint{DSN: "b"},
		Tables: []TableCfg{{
			Name: "t", TimeColumn: "c", Retention: "1d", Filter: "WHERE x=1",
		}},
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected filter validation error")
	}
}

func TestCutoff(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	cut, err := Cutoff("90d", now)
	if err != nil {
		t.Fatal(err)
	}
	want := now.Add(-90 * 24 * time.Hour)
	if !cut.Equal(want) {
		t.Fatalf("got %v want %v", cut, want)
	}
}
