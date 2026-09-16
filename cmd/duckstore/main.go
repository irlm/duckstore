// Command duckstore is the single entry point of the project:
//
//	duckstore seed      create the Postgres schema and load fake data
//	duckstore etl       build the DuckDB warehouse from Postgres
//	duckstore serve     run the store UI (Postgres) and analytics UI (DuckDB)
//	duckstore bench     compare Postgres and DuckDB on the same questions
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/irlm/duckstore/internal/bench"
	"github.com/irlm/duckstore/internal/config"
	"github.com/irlm/duckstore/internal/pg"
	"github.com/irlm/duckstore/internal/query"
	"github.com/irlm/duckstore/internal/seed"
	"github.com/irlm/duckstore/internal/sqlsplit"
	"github.com/irlm/duckstore/internal/warehouse"
	"github.com/irlm/duckstore/internal/web"
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
	case "etl":
		err = runETL(ctx, cfg)
	case "sql":
		err = runSQL(ctx, cfg, os.Args[2:])
	case "serve":
		err = runServe(ctx, cfg)
	case "bench":
		err = runBench(ctx, cfg, os.Args[2:])
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
  seed    create the Postgres schema and load fake data
  etl     build the DuckDB warehouse from Postgres
  sql     run SQL on the warehouse (DuckDB) or the store (Postgres)
  serve   run the web app: store (Postgres) + analytics (DuckDB)
  bench   run the Postgres vs DuckDB comparisons in the terminal`)
}

func runSeed(ctx context.Context, cfg config.Config, args []string) error {
	return seed.Main(ctx, cfg.PostgresURL, args, os.Stdout)
}

func runETL(ctx context.Context, cfg config.Config) error {
	fmt.Printf("building %s from Postgres\n", cfg.WarehousePath())
	res, err := warehouse.Build(ctx, cfg, func(s warehouse.Step) {
		fmt.Printf("  %-45s %12s rows  %8s\n", s.Label, fmtInt(s.Rows), s.Duration.Round(time.Millisecond))
	})
	if err != nil {
		return err
	}
	fmt.Printf("✔ warehouse ready in %s: %.0f MB, orders up to id %d\n", res.Duration.Round(time.Millisecond), float64(res.SizeBytes)/1e6, res.MaxOrderID)
	return nil
}

func fmtInt(n int64) string {
	s := fmt.Sprint(n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func runSQL(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("sql", flag.ExitOnError)
	engine := fs.String("engine", query.EngineDuckDB, "duckdb (warehouse) or postgres (store)")
	file := fs.String("f", "", "read SQL from a file instead of the argument")
	maxRows := fs.Int("max", 40, "maximum rows to print per statement")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: duckstore sql [-engine duckdb|postgres] [-f file.sql] [\"SELECT ...\"]")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	script := strings.Join(fs.Args(), " ")
	if *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		script = string(b)
	}
	if strings.TrimSpace(script) == "" {
		fs.Usage()
		return nil
	}

	var run func(q string) (*query.Result, error)
	switch *engine {
	case query.EngineDuckDB:
		wh := warehouse.New(cfg)
		if err := wh.Reload(ctx); err != nil {
			return err
		}
		defer wh.Close()
		run = func(q string) (res *query.Result, err error) {
			err = wh.With(func(db *sql.DB) error {
				res, err = query.DuckDB(ctx, db, q, *maxRows)
				return err
			})
			return res, err
		}
	case query.EnginePostgres:
		pool, err := pg.Connect(ctx, cfg.PostgresURL, nil)
		if err != nil {
			return err
		}
		defer pool.Close()
		run = func(q string) (*query.Result, error) { return query.Postgres(ctx, pool, q, *maxRows) }
	default:
		return fmt.Errorf("unknown engine %q", *engine)
	}

	for _, stmt := range sqlsplit.Split(script) {
		res, err := run(stmt.SQL)
		if err != nil {
			return fmt.Errorf("%s\n%w", firstLine(stmt.SQL), err)
		}
		printResult(res)
	}
	return nil
}

func printResult(r *query.Result) {
	if len(r.Columns) > 0 {
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, strings.Join(r.Columns, "\t"))
		for _, row := range r.Rows {
			cells := make([]string, len(row))
			for i, v := range row {
				cells[i] = query.Format(v)
			}
			fmt.Fprintln(tw, strings.Join(cells, "\t"))
		}
		tw.Flush()
	}
	more := ""
	if r.Truncated {
		more = fmt.Sprintf(" (showing %d)", len(r.Rows))
	}
	fmt.Printf("-- %s: %d rows%s in %s\n\n", r.Engine, r.RowCount, more, r.Duration.Round(10*time.Microsecond))
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func runServe(ctx context.Context, cfg config.Config) error {
	pool, err := pg.Connect(ctx, cfg.PostgresURL, web.Tracer())
	if err != nil {
		return err
	}
	defer pool.Close()

	wh := warehouse.New(cfg)
	if err := wh.Reload(ctx); err != nil {
		return err
	}
	defer wh.Close()
	go wh.Watch(ctx, 3*time.Second)

	srv, err := web.New(cfg, pool, wh)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdown)
	}()
	log.Printf("duckstore: store http://%s/  analytics http://%s/analytics", cfg.Listen, cfg.Listen)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runBench(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	only := fs.String("only", "", "run one scenario id (default: all)")
	fs.Parse(args)

	pool, err := pg.Connect(ctx, cfg.PostgresURL, nil)
	if err != nil {
		return err
	}
	defer pool.Close()
	wh := warehouse.New(cfg)
	if err := wh.Reload(ctx); err != nil {
		return err
	}
	defer wh.Close()
	env := bench.Env{PG: pool, WH: wh, DataDir: cfg.DataDir}

	for _, sc := range bench.Scenarios() {
		if *only != "" && sc.ID != *only {
			continue
		}
		fmt.Printf("\n▶ %s\n  %s\n", sc.Title, sc.ID)
		res, err := bench.Run(ctx, env, sc)
		if err != nil {
			return err
		}
		best := res.Fastest()
		for i, m := range res.Measurements {
			line := fmt.Sprintf("  %-28s %12s", m.Variant.Name(), m.Median.Round(10*time.Microsecond))
			switch {
			case m.Err != "":
				line = fmt.Sprintf("  %-28s error: %s", m.Variant.Name(), m.Err)
			case sc.Kind == bench.KindWrite:
				line += fmt.Sprintf("  %8.0f ops/s", float64(m.Ops)/m.Median.Seconds())
				if m.Errors > 0 {
					line += fmt.Sprintf("  %d failed (conflicts)", m.Errors)
				}
			case i != best && best >= 0:
				line += fmt.Sprintf("  %6.1fx slower", m.Median.Seconds()/res.Measurements[best].Median.Seconds())
			case i == best:
				line += "  fastest"
			}
			fmt.Println(line)
		}
	}
	return nil
}
