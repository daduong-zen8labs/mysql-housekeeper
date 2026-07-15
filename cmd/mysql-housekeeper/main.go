// Command mysql-housekeeper moves expired MySQL rows from a primary database
// to a housekeeping (archive) database.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/daduong-zen8labs/mysql-housekeeper/internal/config"
	"github.com/daduong-zen8labs/mysql-housekeeper/internal/mover"
	mysqlutil "github.com/daduong-zen8labs/mysql-housekeeper/internal/mysql"
)

// version is set by GoReleaser via -ldflags.
var version = "dev"

const (
	exitOK      = 0
	exitRuntime = 1
	exitConfig  = 2
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 1 {
		printUsage()
		return exitConfig
	}
	cmd := args[0]
	switch cmd {
	case "version":
		fmt.Println(versionString())
		return exitOK
	case "run", "plan":
		return runCmd(cmd, args[1:])
	case "help", "-h", "--help":
		printUsage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		printUsage()
		return exitConfig
	}
}

func runCmd(cmd string, args []string) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("c", "", "path to housekeeper YAML config")
	dryRun := fs.Bool("dry-run", false, "estimate/select only; do not insert or delete (run)")
	table := fs.String("table", "", "process only this table name")
	mode := fs.String("mode", "", "override mode: move|copy|delete")
	runKey := fs.String("run-key", "", "stable run key for checkpoints / resume")
	resume := fs.Bool("resume", false, "continue from last checkpoint for --run-key / defaults.run_key")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "-c config path is required")
		return exitConfig
	}
	if *mode != "" {
		if err := config.ValidateMode(*mode); err != nil {
			fmt.Fprintf(os.Stderr, "invalid --mode: %v\n", err)
			return exitConfig
		}
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return exitConfig
	}
	if *resume {
		key := strings.TrimSpace(*runKey)
		if key == "" {
			key = strings.TrimSpace(cfg.Defaults.RunKey)
		}
		if key == "" {
			fmt.Fprintln(os.Stderr, "--resume requires --run-key or defaults.run_key in config")
			return exitConfig
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	primary, err := mysqlutil.Open(ctx, cfg.Primary.DSN, cfg.Defaults.MaxExecTimeMS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primary connect: %v\n", err)
		return exitRuntime
	}
	defer func() { _ = primary.Close() }()

	house, err := mysqlutil.Open(ctx, cfg.Housekeeping.DSN, cfg.Defaults.MaxExecTimeMS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "housekeeping connect: %v\n", err)
		return exitRuntime
	}
	defer func() { _ = house.Close() }()

	engine, err := mover.New(ctx, primary, house, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engine: %v\n", err)
		return exitRuntime
	}

	opts := mover.Options{
		DryRun:      *dryRun,
		TableFilter: *table,
		Mode:        *mode,
		RunKey:      *runKey,
		Resume:      *resume,
		Logger:      logger,
		Now:         time.Now,
	}

	switch cmd {
	case "plan":
		results, err := engine.Plan(ctx, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "plan failed: %v\n", err)
			return exitRuntime
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
		return exitOK
	case "run":
		result, err := engine.Run(ctx, opts)
		if result != nil {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(result)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
			return exitRuntime
		}
		return exitOK
	}
	return exitConfig
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `mysql-housekeeper — move expired MySQL rows to a housekeeping database

Usage:
  mysql-housekeeper run  -c housekeeper.yaml [--dry-run] [--table name] [--mode move|copy|delete] [--run-key NAME] [--resume]
  mysql-housekeeper plan -c housekeeper.yaml [--table name] [--mode move|copy|delete]
  mysql-housekeeper version

Exit codes: 0 ok, 1 runtime error, 2 config/validation error
`)
}

func versionString() string {
	if version != "" && version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
