package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"time"

	"github.com/nudgeworks/mysql-housekeeper/internal/config"
	"github.com/nudgeworks/mysql-housekeeper/internal/mover"
	mysqlutil "github.com/nudgeworks/mysql-housekeeper/internal/mysql"
)

const (
	exitOK           = 0
	exitRuntime      = 1
	exitConfig       = 2
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
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "-c config path is required")
		return exitConfig
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return exitConfig
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	primary, err := mysqlutil.Open(ctx, cfg.Primary.DSN, cfg.Defaults.MaxExecTimeMS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "primary connect: %v\n", err)
		return exitRuntime
	}
	defer primary.Close()

	house, err := mysqlutil.Open(ctx, cfg.Housekeeping.DSN, cfg.Defaults.MaxExecTimeMS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "housekeeping connect: %v\n", err)
		return exitRuntime
	}
	defer house.Close()

	engine, err := mover.New(ctx, primary, house, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engine: %v\n", err)
		return exitRuntime
	}

	opts := mover.Options{
		DryRun:      *dryRun,
		TableFilter: *table,
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
  mysql-housekeeper run  -c housekeeper.yaml [--dry-run] [--table name]
  mysql-housekeeper plan -c housekeeper.yaml [--table name]
  mysql-housekeeper version

Exit codes: 0 ok, 1 runtime error, 2 config/validation error
`)
}

func versionString() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
