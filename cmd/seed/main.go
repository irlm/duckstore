// Command seed only loads the fake store data into Postgres. Unlike cmd/duckstore
// it does not link DuckDB, so it builds without CGO into a small Docker image.
//
//	seed -scale 5
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/irlm/duckstore/internal/config"
	"github.com/irlm/duckstore/internal/seed"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := seed.Main(ctx, config.Load().PostgresURL, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
