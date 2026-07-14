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
	BatchSize      int  `yaml:"batch_size"`
	MaxRowsPerRun  int  `yaml:"max_rows_per_run"`
	DryRun         bool `yaml:"dry_run"`
	ThrottleMS     int  `yaml:"throttle_ms"`
	MaxExecTimeMS  int  `yaml:"max_execution_time_ms"`
}

// TableCfg describes one table retention policy.
type TableCfg struct {
	Name         string   `yaml:"name"`
	TimeColumn   string   `yaml:"time_column"`
	Retention    string   `yaml:"retention"`
	Filter       string   `yaml:"filter"`
	PrimaryKey   []string `yaml:"primary_key"`
	BatchSize    *int     `yaml:"batch_size"`
	MaxRowsPerRun *int    `yaml:"max_rows_per_run"`
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
		if len(sub) != 2 {
			return m
		}
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
}

// Validate checks required fields and retention formats.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.Primary.DSN) == "" {
		return fmt.Errorf("primary.dsn is required")
	}
	if strings.TrimSpace(c.Housekeeping.DSN) == "" {
		return fmt.Errorf("housekeeping.dsn is required")
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
		if _, err := ParseRetention(t.Retention); err != nil {
			return fmt.Errorf("tables[%d].retention: %w", i, err)
		}
		if t.Filter != "" && (strings.HasPrefix(strings.ToUpper(strings.TrimSpace(t.Filter)), "WHERE") ||
			strings.ContainsAny(t.Filter, ";")) {
			return fmt.Errorf("tables[%d].filter must not start with WHERE or contain ';'", i)
		}
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

// ParseRetention parses values like 90d, 12h, 30m, 60s into a duration.
func ParseRetention(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty retention")
	}
	m := retentionRe.FindStringSubmatch(s)
	if m == nil {
		// Fall back to Go duration (e.g. 90h0m0s) for tests / advanced use.
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid retention %q (want Nd/Nh/Nm/Ns)", s)
		}
		if d <= 0 {
			return 0, fmt.Errorf("retention must be positive")
		}
		return d, nil
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
	default:
		return 0, fmt.Errorf("invalid unit")
	}
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
