package seed

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/irlm/duckstore/internal/pg"
)

// Main parses the seed flags and runs the generator. It is shared by
// `duckstore seed` and the small seed-only binary used in Docker (cmd/seed).
func Main(ctx context.Context, postgresURL string, args []string, out io.Writer) error {
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

	pool, err := pg.Connect(ctx, postgresURL, nil)
	if err != nil {
		return err
	}
	defer pool.Close()

	return Run(ctx, pool, Options{Scale: *scale, Seed: *seedVal, End: end, Years: *years, Log: out})
}
