// Package config loads and validates mysql-housekeeper YAML configuration.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Run modes.
const (
	ModeMove   = "move"   // insert into housekeeping, then delete from primary
	ModeCopy   = "copy"   // insert only (leave primary)
	ModeDelete = "delete" // purge from primary only (no archive write)
)

// On-conflict strategies for INSERT into housekeeping.
const (
	ConflictIgnore = "ignore" // INSERT IGNORE
	ConflictFail   = "fail"   // plain INSERT (error on duplicate)
)

// Config is the top-level housekeeper configuration.
type Config struct {
	Primary      Endpoint   `yaml:"primary"`
	Housekeeping Endpoint   `yaml:"housekeeping"`
	Defaults     Defaults   `yaml:"defaults"`
	Tables       []TableCfg `yaml:"tables"`
}

// Endpoint holds a MySQL DSN (may contain ${ENV} placeholders).
type Endpoint struct {
	DSN string `yaml:"dsn"`
}

// Defaults apply when a table does not override them.
type Defaults struct {
	BatchSize     int    `yaml:"batch_size"`
	MaxRowsPerRun int    `yaml:"max_rows_per_run"`
	DryRun        bool   `yaml:"dry_run"`
	ThrottleMS    int    `yaml:"throttle_ms"`
	MaxExecTimeMS int    `yaml:"max_execution_time_ms"`
	Mode          string `yaml:"mode"`         // move|copy|delete
	OnConflict    string `yaml:"on_conflict"` // ignore|fail
}

// TableCfg describes one table retention policy.
type TableCfg struct {
	Name          string   `yaml:"name"`
	TargetTable   string   `yaml:"target_table"` // housekeeping table name; default = name
	TimeColumn    string   `yaml:"time_column"`
	Retention     string   `yaml:"retention"` // Nd/Nh/Nm/Ns; mutually exclusive with before
	Before        string   `yaml:"before"`    // absolute cutoff: RFC3339 or YYYY-MM-DD (UTC)
	Filter        string   `yaml:"filter"`    // single AND clause
	Filters       []string `yaml:"filters"`   // additional AND clauses
	Enabled       *bool    `yaml:"enabled"`   // default true
	Mode          *string  `yaml:"mode"`
	OnConflict    *string  `yaml:"on_conflict"`
	PrimaryKey    []string `yaml:"primary_key"`
	BatchSize     *int     `yaml:"batch_size"`
	MaxRowsPerRun *int     `yaml:"max_rows_per_run"`
}

var envPlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
var retentionRe = regexp.MustCompile(`^(\d+)([dhms])$`)

// Load reads and validates a YAML config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	expanded := expandEnv(string(raw))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func expandEnv(s string) string {
	return envPlaceholder.ReplaceAllStringFunc(s, func(m string) string {
		sub := envPlaceholder.FindStringSubmatch(m)
		if v, ok := os.LookupEnv(sub[1]); ok {
			return v
		}
		return m
	})
}

func (c *Config) applyDefaults() {
	if c.Defaults.BatchSize <= 0 {
		c.Defaults.BatchSize = 1000
	}
	if c.Defaults.MaxRowsPerRun <= 0 {
		c.Defaults.MaxRowsPerRun = 500000
	}
	if c.Defaults.Mode == "" {
		c.Defaults.Mode = ModeMove
	}
	if c.Defaults.OnConflict == "" {
		c.Defaults.OnConflict = ConflictIgnore
	}
}

// Validate checks required fields and policy formats.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Primary.DSN) == "" {
		return fmt.Errorf("primary.dsn is required")
	}
	if strings.TrimSpace(c.Housekeeping.DSN) == "" {
		return fmt.Errorf("housekeeping.dsn is required")
	}
	if err := validateMode(c.Defaults.Mode); err != nil {
		return fmt.Errorf("defaults.mode: %w", err)
	}
	if err := validateConflict(c.Defaults.OnConflict); err != nil {
		return fmt.Errorf("defaults.on_conflict: %w", err)
	}
	if len(c.Tables) == 0 {
		return fmt.Errorf("tables: at least one table is required")
	}
	seen := map[string]struct{}{}
	for i, t := range c.Tables {
		if strings.TrimSpace(t.Name) == "" {
			return fmt.Errorf("tables[%d].name is required", i)
		}
		if _, ok := seen[t.Name]; ok {
			return fmt.Errorf("tables: duplicate name %q", t.Name)
		}
		seen[t.Name] = struct{}{}
		if strings.TrimSpace(t.TimeColumn) == "" {
			return fmt.Errorf("tables[%d].time_column is required", i)
		}
		hasRet := strings.TrimSpace(t.Retention) != ""
		hasBefore := strings.TrimSpace(t.Before) != ""
		if hasRet == hasBefore {
			return fmt.Errorf("tables[%d]: set exactly one of retention or before", i)
		}
		if hasRet {
			if _, err := ParseRetention(t.Retention); err != nil {
				return fmt.Errorf("tables[%d].retention: %w", i, err)
			}
		}
		if hasBefore {
			if _, err := ParseBefore(t.Before); err != nil {
				return fmt.Errorf("tables[%d].before: %w", i, err)
			}
		}
		if err := validateFilterExpr(t.Filter); err != nil {
			return fmt.Errorf("tables[%d].filter: %w", i, err)
		}
		for j, f := range t.Filters {
			if err := validateFilterExpr(f); err != nil {
				return fmt.Errorf("tables[%d].filters[%d]: %w", i, j, err)
			}
		}
		if t.Mode != nil {
			if err := validateMode(*t.Mode); err != nil {
				return fmt.Errorf("tables[%d].mode: %w", i, err)
			}
		}
		if t.OnConflict != nil {
			if err := validateConflict(*t.OnConflict); err != nil {
				return fmt.Errorf("tables[%d].on_conflict: %w", i, err)
			}
		}
	}
	return nil
}

func validateMode(m string) error {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case ModeMove, ModeCopy, ModeDelete:
		return nil
	default:
		return fmt.Errorf("invalid %q (want move|copy|delete)", m)
	}
}

func validateConflict(c string) error {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case ConflictIgnore, ConflictFail:
		return nil
	default:
		return fmt.Errorf("invalid %q (want ignore|fail)", c)
	}
}

func validateFilterExpr(f string) error {
	f = strings.TrimSpace(f)
	if f == "" {
		return nil
	}
	if strings.HasPrefix(strings.ToUpper(f), "WHERE") || strings.ContainsAny(f, ";") {
		return fmt.Errorf("must not start with WHERE or contain ';'")
	}
	return nil
}

// BatchSizeFor returns the effective batch size for a table.
func (c *Config) BatchSizeFor(t TableCfg) int {
	if t.BatchSize != nil && *t.BatchSize > 0 {
		return *t.BatchSize
	}
	return c.Defaults.BatchSize
}

// MaxRowsFor returns the effective max rows per run for a table.
func (c *Config) MaxRowsFor(t TableCfg) int {
	if t.MaxRowsPerRun != nil && *t.MaxRowsPerRun > 0 {
		return *t.MaxRowsPerRun
	}
	return c.Defaults.MaxRowsPerRun
}

// ModeFor returns effective run mode for a table.
func (c *Config) ModeFor(t TableCfg) string {
	if t.Mode != nil && *t.Mode != "" {
		return strings.ToLower(strings.TrimSpace(*t.Mode))
	}
	m := strings.ToLower(strings.TrimSpace(c.Defaults.Mode))
	if m == "" {
		return ModeMove
	}
	return m
}

// ConflictFor returns effective on_conflict for a table.
func (c *Config) ConflictFor(t TableCfg) string {
	if t.OnConflict != nil && *t.OnConflict != "" {
		return strings.ToLower(strings.TrimSpace(*t.OnConflict))
	}
	v := strings.ToLower(strings.TrimSpace(c.Defaults.OnConflict))
	if v == "" {
		return ConflictIgnore
	}
	return v
}

// IsEnabled reports whether the table policy is active.
func (t TableCfg) IsEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// DestName is the housekeeping table name.
func (t TableCfg) DestName() string {
	if s := strings.TrimSpace(t.TargetTable); s != "" {
		return s
	}
	return t.Name
}

// WhereClauses returns AND filter expressions (no leading WHERE).
func (t TableCfg) WhereClauses() []string {
	var out []string
	if f := strings.TrimSpace(t.Filter); f != "" {
		out = append(out, f)
	}
	for _, f := range t.Filters {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// ParseRetention parses values like 90d, 12h, 30m, 60s into a duration.
func ParseRetention(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty retention")
	}
	m := retentionRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid retention %q (want Nd/Nh/Nm/Ns)", s)
	}
	n, _ := strconv.Atoi(m[1])
	if n <= 0 {
		return 0, fmt.Errorf("retention must be positive")
	}
	switch m[2] {
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "h":
		return time.Duration(n) * time.Hour, nil
	case "m":
		return time.Duration(n) * time.Minute, nil
	case "s":
		return time.Duration(n) * time.Second, nil
	}
	return 0, fmt.Errorf("invalid retention %q (want Nd/Nh/Nm/Ns)", s)
}

// ParseBefore parses an absolute cutoff (RFC3339 or YYYY-MM-DD as UTC midnight).
func ParseBefore(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty before")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.UTC); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid before %q (want RFC3339 or YYYY-MM-DD)", s)
}

// CutoffFor returns the expiry cutoff for a table policy.
func CutoffFor(t TableCfg, now time.Time) (time.Time, error) {
	if strings.TrimSpace(t.Before) != "" {
		return ParseBefore(t.Before)
	}
	d, err := ParseRetention(t.Retention)
	if err != nil {
		return time.Time{}, err
	}
	return now.UTC().Add(-d), nil
}

// Cutoff returns now(UTC) - retention.
func Cutoff(retention string, now time.Time) (time.Time, error) {
	d, err := ParseRetention(retention)
	if err != nil {
		return time.Time{}, err
	}
	return now.UTC().Add(-d), nil
}

// FilterTables returns tables matching name, or all if name is empty.
func (c *Config) FilterTables(name string) ([]TableCfg, error) {
	if name == "" {
		return c.Tables, nil
	}
	for _, t := range c.Tables {
		if t.Name == name {
			return []TableCfg{t}, nil
		}
	}
	return nil, fmt.Errorf("table %q not found in config", name)
}
