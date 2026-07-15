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

func TestParseBefore(t *testing.T) {
	d, err := ParseBefore("2025-01-15")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	if !d.Equal(want) {
		t.Fatalf("%v", d)
	}
	ts, err := ParseBefore("2025-01-15T12:00:00Z")
	if err != nil || !ts.Equal(time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("%v %v", ts, err)
	}
}

func TestCutoffFor(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	cut, err := CutoffFor(TableCfg{Retention: "90d"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !cut.Equal(now.Add(-90 * 24 * time.Hour)) {
		t.Fatal(cut)
	}
	cut, err = CutoffFor(TableCfg{Before: "2025-06-01"}, now)
	if err != nil || !cut.Equal(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%v %v", cut, err)
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
  mode: copy
  on_conflict: fail
tables:
  - name: logs
    time_column: created_at
    retention: 7d
    filter: "status = 'done'"
    filters:
      - "tenant_id > 0"
    target_table: logs_archive
    enabled: true
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
	if cfg.Defaults.Mode != ModeCopy || cfg.Defaults.OnConflict != ConflictFail {
		t.Fatalf("%+v", cfg.Defaults)
	}
	t0 := cfg.Tables[0]
	if t0.DestName() != "logs_archive" || len(t0.WhereClauses()) != 2 {
		t.Fatalf("%+v", t0)
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

func TestValidateRequiresRetentionXorBefore(t *testing.T) {
	cfg := &Config{
		Primary:      Endpoint{DSN: "a"},
		Housekeeping: Endpoint{DSN: "b"},
		Tables:       []TableCfg{{Name: "t", TimeColumn: "c"}},
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected retention/before error")
	}
	cfg.Tables[0].Retention = "1d"
	cfg.Tables[0].Before = "2020-01-01"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected xor error")
	}
}

func TestBatchSizeAndMaxRowsHelpers(t *testing.T) {
	cfg := &Config{Defaults: Defaults{BatchSize: 1000, MaxRowsPerRun: 5000, Mode: ModeMove, OnConflict: ConflictIgnore}}
	bs := 10
	mr := 20
	if cfg.BatchSizeFor(TableCfg{BatchSize: &bs}) != 10 {
		t.Fatal("batch override")
	}
	if cfg.MaxRowsFor(TableCfg{MaxRowsPerRun: &mr}) != 20 {
		t.Fatal("max override")
	}
	if cfg.BatchSizeFor(TableCfg{}) != 1000 || cfg.MaxRowsFor(TableCfg{}) != 5000 {
		t.Fatal("defaults")
	}
	mode := ModeDelete
	conf := ConflictFail
	if cfg.ModeFor(TableCfg{Mode: &mode}) != ModeDelete {
		t.Fatal("mode override")
	}
	if cfg.ConflictFor(TableCfg{OnConflict: &conf}) != ConflictFail {
		t.Fatal("conflict override")
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

func TestFilterTables(t *testing.T) {
	cfg := &Config{
		Tables: []TableCfg{{Name: "a"}, {Name: "b"}},
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

func TestIsEnabled(t *testing.T) {
	if !(TableCfg{}).IsEnabled() {
		t.Fatal("default enabled")
	}
	f := false
	if (TableCfg{Enabled: &f}).IsEnabled() {
		t.Fatal("should be disabled")
	}
}
