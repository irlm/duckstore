// Package bench runs the same question on Postgres and DuckDB and measures
// them. The web "engine race" page and `duckstore bench` both use it.
package bench

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irlm/duckstore/internal/query"
	"github.com/irlm/duckstore/internal/warehouse"
)

const (
	KindAnalytics = "analytics"
	KindLookup    = "lookup"
	KindWrite     = "write"
)

type Env struct {
	PG      *pgxpool.Pool
	WH      *warehouse.Warehouse
	DataDir string
}

type Scenario struct {
	ID       string
	Kind     string
	Title    string
	Question string
	Why      string
	Args     func(ctx context.Context, env Env) ([]any, error)
	Variants []Variant
}

type Variant struct {
	Key   string
	Label string
	SQL   string
	// Exec is set for write scenarios. It prepares its own tables and returns
	// the time of the measured part only.
	Exec func(ctx context.Context, env Env, sql string) (elapsed time.Duration, ops, errs int, err error)
}

func (v Variant) Engine() string {
	if v.Key == KeyPostgres {
		return query.EnginePostgres
	}
	return query.EngineDuckDB
}

func (v Variant) Name() string {
	if v.Label != "" {
		return v.Label
	}
	switch v.Key {
	case KeyPostgres:
		return "Postgres (store tables)"
	case KeyDuckDBRaw:
		return "DuckDB (same tables)"
	case KeyDuckDBStar:
		return "DuckDB (star schema)"
	}
	return v.Key
}

type Measurement struct {
	Variant Variant
	Median  time.Duration
	Runs    []time.Duration
	Rows    int
	Ops     int
	Errors  int
	Err     string
	Sample  *query.Result
}

type Result struct {
	Scenario     *Scenario
	Args         []any
	At           time.Time
	Measurements []Measurement
}

// Fastest returns the index of the fastest successful measurement, or -1.
func (r *Result) Fastest() int {
	best := -1
	for i, m := range r.Measurements {
		if m.Err == "" && (best < 0 || m.Median < r.Measurements[best].Median) {
			best = i
		}
	}
	return best
}

func (r *Result) Slowest() time.Duration {
	var d time.Duration
	for _, m := range r.Measurements {
		d = max(d, m.Median)
	}
	return d
}

func Scenarios() []*Scenario { return scenarios }

func Find(id string) *Scenario {
	for _, s := range scenarios {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Run measures every variant of a scenario, one after the other.
func Run(ctx context.Context, env Env, sc *Scenario) (*Result, error) {
	res := &Result{Scenario: sc, At: time.Now()}
	if sc.Args != nil {
		args, err := sc.Args(ctx, env)
		if err != nil {
			return nil, err
		}
		res.Args = args
	}
	for _, v := range sc.Variants {
		var m Measurement
		if v.Exec != nil {
			m = runWrite(ctx, env, v)
		} else {
			m = runRead(ctx, env, v, res.Args)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		res.Measurements = append(res.Measurements, m)
	}
	return res, nil
}

// runRead does one warm-up run (caches, plans), then times 5 runs, or only
// 1 run when the warm-up took more than 2 seconds. The median is reported.
func runRead(ctx context.Context, env Env, v Variant, args []any) Measurement {
	m := Measurement{Variant: v}
	once := func() (*query.Result, error) {
		if v.Engine() == query.EnginePostgres {
			return query.PostgresReadOnly(ctx, env.PG, v.SQL, 10, 10*time.Minute, args...)
		}
		var res *query.Result
		err := env.WH.With(func(db *sql.DB) error {
			var err error
			res, err = query.DuckDB(ctx, db, v.SQL, 10, args...)
			return err
		})
		return res, err
	}
	warm, err := once()
	if err != nil {
		m.Err = err.Error()
		return m
	}
	runs := 5
	if warm.Duration > 2*time.Second {
		runs = 1
	}
	for range runs {
		res, err := once()
		if err != nil {
			m.Err = err.Error()
			return m
		}
		m.Runs = append(m.Runs, res.Duration)
		m.Sample, m.Rows = res, res.RowCount
	}
	m.Median = median(m.Runs)
	return m
}

func runWrite(ctx context.Context, env Env, v Variant) Measurement {
	m := Measurement{Variant: v}
	elapsed, ops, errs, err := v.Exec(ctx, env, v.SQL)
	if err != nil {
		m.Err = err.Error()
		return m
	}
	m.Median, m.Runs, m.Ops, m.Errors = elapsed, []time.Duration{elapsed}, ops, errs
	return m
}

func median(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[len(s)/2]
}

// --- arguments -------------------------------------------------------------------

func warehouseMaxOrderID(ctx context.Context, env Env) (int64, error) {
	var maxID int64
	err := env.WH.With(func(db *sql.DB) error {
		return db.QueryRowContext(ctx, "SELECT max_order_id FROM dw.etl_info").Scan(&maxID)
	})
	return maxID, err
}

func randomOrderID(ctx context.Context, env Env) ([]any, error) {
	maxID, err := warehouseMaxOrderID(ctx, env)
	if err != nil {
		return nil, err
	}
	return []any{1 + rand.Int64N(maxID)}, nil
}

func randomCustomerID(ctx context.Context, env Env) ([]any, error) {
	var id int64
	err := env.PG.QueryRow(ctx, `SELECT customer_id FROM orders WHERE id = $1`, 1+rand.Int64N(2_000_000)).Scan(&id)
	if err != nil {
		err = env.PG.QueryRow(ctx, `SELECT customer_id FROM orders ORDER BY id DESC LIMIT 1`).Scan(&id)
	}
	return []any{id}, err
}

// --- write scenarios ---------------------------------------------------------------

const (
	writeOps     = 2000
	stockRows    = 30000
	hotRows      = 16
	writers      = 8
	opsPerWriter = 150
)

// scratch opens a fresh DuckDB file used only by the write tests.
func scratch(env Env) (*sql.DB, func(), error) {
	path := filepath.Join(env.DataDir, "bench_scratch.duckdb")
	clean := func() {
		os.Remove(path)
		os.Remove(path + ".wal")
	}
	clean()
	if err := os.MkdirAll(env.DataDir, 0o755); err != nil {
		return nil, nil, err
	}
	connector, err := duckdb.NewConnector(path, nil)
	if err != nil {
		return nil, nil, err
	}
	db := sql.OpenDB(connector)
	return db, func() { db.Close(); clean() }, nil
}

func pgSetup(ctx context.Context, env Env, stmts ...string) error {
	for _, s := range append([]string{"CREATE SCHEMA IF NOT EXISTS bench"}, stmts...) {
		if _, err := env.PG.Exec(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

func duckSetup(ctx context.Context, db *sql.DB, stmts ...string) error {
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

var (
	pgEventsDDL   = []string{"DROP TABLE IF EXISTS bench.events", "CREATE TABLE bench.events (id bigint PRIMARY KEY, product_id bigint NOT NULL, quantity int NOT NULL, created_at timestamptz NOT NULL)"}
	duckEventsDDL = []string{"CREATE TABLE events (id BIGINT PRIMARY KEY, product_id BIGINT NOT NULL, quantity INTEGER NOT NULL, created_at TIMESTAMP NOT NULL)"}
	pgStockDDL    = []string{"DROP TABLE IF EXISTS bench.stock", "CREATE TABLE bench.stock (product_id bigint PRIMARY KEY, quantity int NOT NULL)", fmt.Sprintf("INSERT INTO bench.stock SELECT g, 1000000 FROM generate_series(1, %d) g", stockRows), "VACUUM ANALYZE bench.stock"}
	duckStockDDL  = []string{"CREATE TABLE stock (product_id BIGINT PRIMARY KEY, quantity INTEGER NOT NULL)", fmt.Sprintf("INSERT INTO stock SELECT i, 1000000 FROM range(1, %d) t(i)", stockRows+1), "CHECKPOINT"}
)

func pgSingleInserts(ctx context.Context, env Env, q string) (time.Duration, int, int, error) {
	if err := pgSetup(ctx, env, pgEventsDDL...); err != nil {
		return 0, 0, 0, err
	}
	start := time.Now()
	for i := range writeOps {
		if _, err := env.PG.Exec(ctx, q, int64(i+1), rand.Int64N(stockRows)+1, int32(1+rand.IntN(3))); err != nil {
			return 0, 0, 0, err
		}
	}
	return time.Since(start), writeOps, 0, nil
}

func duckSingleInserts(ctx context.Context, env Env, q string) (time.Duration, int, int, error) {
	db, done, err := scratch(env)
	if err != nil {
		return 0, 0, 0, err
	}
	defer done()
	if err := duckSetup(ctx, db, duckEventsDDL...); err != nil {
		return 0, 0, 0, err
	}
	stmt, err := db.PrepareContext(ctx, q)
	if err != nil {
		return 0, 0, 0, err
	}
	defer stmt.Close()
	start := time.Now()
	for i := range writeOps {
		if _, err := stmt.ExecContext(ctx, int64(i+1), rand.Int64N(stockRows)+1, int32(1+rand.IntN(3))); err != nil {
			return 0, 0, 0, err
		}
	}
	return time.Since(start), writeOps, 0, nil
}

func pgSingleUpdates(ctx context.Context, env Env, q string) (time.Duration, int, int, error) {
	if err := pgSetup(ctx, env, pgStockDDL...); err != nil {
		return 0, 0, 0, err
	}
	start := time.Now()
	for range writeOps {
		if _, err := env.PG.Exec(ctx, q, rand.Int64N(stockRows)+1); err != nil {
			return 0, 0, 0, err
		}
	}
	return time.Since(start), writeOps, 0, nil
}

func duckSingleUpdates(ctx context.Context, env Env, q string) (time.Duration, int, int, error) {
	db, done, err := scratch(env)
	if err != nil {
		return 0, 0, 0, err
	}
	defer done()
	if err := duckSetup(ctx, db, duckStockDDL...); err != nil {
		return 0, 0, 0, err
	}
	stmt, err := db.PrepareContext(ctx, q)
	if err != nil {
		return 0, 0, 0, err
	}
	defer stmt.Close()
	start := time.Now()
	for range writeOps {
		if _, err := stmt.ExecContext(ctx, rand.Int64N(stockRows)+1); err != nil {
			return 0, 0, 0, err
		}
	}
	return time.Since(start), writeOps, 0, nil
}

// concurrent runs `writers` goroutines doing `opsPerWriter` updates each on a
// few hot rows. Failed updates are counted, not retried.
func concurrent(ctx context.Context, update func(ctx context.Context, productID int64) error) (time.Duration, int, int, error) {
	var ok, failed atomic.Int64
	var fatal error
	var once sync.Once
	var wg sync.WaitGroup
	start := time.Now()
	for range writers {
		wg.Go(func() {
			for range opsPerWriter {
				err := update(ctx, rand.Int64N(hotRows)+1)
				switch {
				case err == nil:
					ok.Add(1)
				case isConflict(err):
					failed.Add(1)
				default:
					once.Do(func() { fatal = err })
					return
				}
			}
		})
	}
	wg.Wait()
	return time.Since(start), int(ok.Load()), int(failed.Load()), fatal
}

func isConflict(err error) bool {
	var de *duckdb.Error
	if errors.As(err, &de) {
		return de.Type == duckdb.ErrorTypeTransaction
	}
	return false
}

func pgConcurrentUpdates(ctx context.Context, env Env, q string) (time.Duration, int, int, error) {
	if err := pgSetup(ctx, env, pgStockDDL...); err != nil {
		return 0, 0, 0, err
	}
	return concurrent(ctx, func(ctx context.Context, id int64) error {
		_, err := env.PG.Exec(ctx, q, id)
		return err
	})
}

func duckConcurrentUpdates(ctx context.Context, env Env, q string) (time.Duration, int, int, error) {
	db, done, err := scratch(env)
	if err != nil {
		return 0, 0, 0, err
	}
	defer done()
	if err := duckSetup(ctx, db, duckStockDDL...); err != nil {
		return 0, 0, 0, err
	}
	db.SetMaxOpenConns(writers)
	return concurrent(ctx, func(ctx context.Context, id int64) error {
		_, err := db.ExecContext(ctx, q, id)
		return err
	})
}

func pgBulkInsert(ctx context.Context, env Env, q string) (time.Duration, int, int, error) {
	if err := pgSetup(ctx, env, "DROP TABLE IF EXISTS bench.bulk", "CREATE TABLE bench.bulk (id bigint, product_id bigint, quantity int, created_at timestamptz)"); err != nil {
		return 0, 0, 0, err
	}
	start := time.Now()
	tag, err := env.PG.Exec(ctx, q)
	if err != nil {
		return 0, 0, 0, err
	}
	elapsed := time.Since(start)
	_, err = env.PG.Exec(ctx, "DROP TABLE bench.bulk")
	return elapsed, int(tag.RowsAffected()), 0, err
}

func duckBulkInsert(ctx context.Context, env Env, q string) (time.Duration, int, int, error) {
	db, done, err := scratch(env)
	if err != nil {
		return 0, 0, 0, err
	}
	defer done()
	if err := duckSetup(ctx, db, "CREATE TABLE bulk (id BIGINT, product_id BIGINT, quantity INTEGER, created_at TIMESTAMP)"); err != nil {
		return 0, 0, 0, err
	}
	start := time.Now()
	// Autocommit on both sides: the time includes the commit.
	r, err := db.ExecContext(ctx, q)
	if err != nil {
		return 0, 0, 0, err
	}
	n, _ := r.RowsAffected()
	return time.Since(start), int(n), 0, nil
}
