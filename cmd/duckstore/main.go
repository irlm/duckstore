// Command duckstore is the single entry point of the project:
//
//	duckstore seed      create the Postgres schema and load fake data
//	duckstore etl       build the DuckDB warehouse from Postgres
//	duckstore serve     run the store UI (Postgres) and analytics UI (DuckDB)
//	duckstore bench     compare Postgres and DuckDB on the same questions
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/irlm/duckstore/internal/config"
	"github.com/irlm/duckstore/internal/pg"
	"github.com/irlm/duckstore/internal/seed"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	var err error
	switch os.Args[1] {
	case "seed":
		err = runSeed(ctx, cfg, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: duckstore <command> [flags]

commands:
  seed    create the Postgres schema and load fake data`)
}

func runSeed(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	scale := fs.Float64("scale", 1, "data size: 1 = ~2.5M orders, 0.1 = ~250k orders")
	seedVal := fs.Uint64("seed", 42, "random seed (same seed = same data)")
	years := fs.Int("years", 3, "years of order history")
	endStr := fs.String("end", "", "last day of history, YYYY-MM-DD (default: today)")
	fs.Parse(args)

	end := time.Now().UTC()
	if *endStr != "" {
		t, err := time.Parse(time.DateOnly, *endStr)
		if err != nil {
			return fmt.Errorf("-end: %w", err)
		}
		end = t
	}

	pool, err := pg.Connect(ctx, cfg.PostgresURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	return seed.Run(ctx, pool, seed.Options{Scale: *scale, Seed: *seedVal, End: end, Years: *years, Log: os.Stdout})
}
