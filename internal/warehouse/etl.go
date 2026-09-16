// Package warehouse builds and reads the DuckDB analytics warehouse.
package warehouse

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/duckdb/duckdb-go/v2"

	"github.com/irlm/duckstore/internal/config"
	"github.com/irlm/duckstore/internal/sqlsplit"
)

//go:embed etl/*.sql
var etlFS embed.FS

type Step struct {
	Label    string
	Rows     int64
	Duration time.Duration
}

type BuildResult struct {
	MaxOrderID int64
	Steps      []Step
	Duration   time.Duration
	SizeBytes  int64
}

var ErrETLRunning = errors.New("another ETL run is already building the warehouse")

// Build creates a brand-new warehouse file from Postgres and then swaps it in
// place of the old one (pattern: "build a new file, then swap").
//
// Readers keep using the old file until the swap, and a failed build never
// breaks the current warehouse. DuckDB allows only one writer per file, so
// the ETL writes to a separate ".building" file that nobody else opens.
func Build(ctx context.Context, cfg config.Config, onStep func(Step)) (*BuildResult, error) {
	began := time.Now()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	unlock, err := lockFile(filepath.Join(cfg.DataDir, "etl.lock"))
	if err != nil {
		return nil, ErrETLRunning
	}
	defer unlock()

	final := cfg.WarehousePath()
	building := final + ".building"
	parquetBuilding := cfg.ParquetDir() + ".building"
	for _, p := range []string{building, building + ".wal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if err := os.RemoveAll(parquetBuilding); err != nil {
		return nil, err
	}

	connector, err := duckdb.NewConnector(building, nil)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	// One connection: SET, LOAD and ATTACH are per session, and every
	// statement below must see them.
	db.SetMaxOpenConns(1)
	defer db.Close()

	res := &BuildResult{}
	run := func(label, query string) error {
		t := time.Now()
		r, err := db.ExecContext(ctx, query)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		st := Step{Label: label, Duration: time.Since(t)}
		st.Rows, _ = r.RowsAffected()
		if m := createTableRe.FindStringSubmatch(query); m != nil {
			// CREATE TABLE AS does not report a row count; count(*) is
			// answered from table metadata, so this is instant.
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+m[1]).Scan(&st.Rows); err != nil {
				return fmt.Errorf("%s: count rows: %w", label, err)
			}
		}
		if label != "" {
			res.Steps = append(res.Steps, st)
			if onStep != nil {
				onStep(st)
			}
		}
		return nil
	}

	// Setup: UTC session, postgres extension, attach the store read-only.
	setup := []string{
		"SET TimeZone = 'UTC'",
		"INSTALL postgres",
		"LOAD postgres",
		fmt.Sprintf("ATTACH %s AS pg (TYPE postgres, READ_ONLY)", quote(cfg.PostgresURL)),
	}
	for _, q := range setup {
		if err := run("", q); err != nil {
			return nil, fmt.Errorf("setup: %w", err)
		}
	}
	if err := db.QueryRowContext(ctx, "SELECT coalesce(max(id), 0) FROM pg.store.orders").Scan(&res.MaxOrderID); err != nil {
		return nil, fmt.Errorf("read watermark: %w", err)
	}

	files, err := fs.Glob(etlFS, "etl/*.sql")
	if err != nil {
		return nil, err
	}
	for _, f := range files { // Glob returns sorted names: 01_, 02_, ...
		script, err := etlFS.ReadFile(f)
		if err != nil {
			return nil, err
		}
		text := strings.ReplaceAll(string(script), "{{MAX_ORDER_ID}}", strconv.FormatInt(res.MaxOrderID, 10))
		for _, stmt := range sqlsplit.Split(text) {
			if err := run(stmt.Label, stmt.SQL); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
		}
	}

	// Export to Parquet: the same data as open files any tool can read
	// (DuckDB, Spark, pandas, Power BI...). fact_sales is partitioned by
	// year/month into folders like fact_sales/year=2025/month=11/.
	if err := os.MkdirAll(parquetBuilding, 0o755); err != nil {
		return nil, err
	}
	exports := []struct{ label, query string }{
		{"parquet: fact_sales (partitioned)", fmt.Sprintf(
			"COPY (SELECT *, year(order_date) AS year, month(order_date) AS month FROM dw.fact_sales) TO %s (FORMAT parquet, PARTITION_BY (year, month), COMPRESSION zstd)",
			quote(filepath.Join(parquetBuilding, "fact_sales")))},
		{"parquet: fact_orders", fmt.Sprintf("COPY dw.fact_orders TO %s (FORMAT parquet, COMPRESSION zstd)", quote(filepath.Join(parquetBuilding, "fact_orders.parquet")))},
		{"parquet: dim_product", fmt.Sprintf("COPY dw.dim_product TO %s (FORMAT parquet)", quote(filepath.Join(parquetBuilding, "dim_product.parquet")))},
		{"parquet: dim_customer", fmt.Sprintf("COPY dw.dim_customer TO %s (FORMAT parquet)", quote(filepath.Join(parquetBuilding, "dim_customer.parquet")))},
		{"parquet: dim_category", fmt.Sprintf("COPY dw.dim_category TO %s (FORMAT parquet)", quote(filepath.Join(parquetBuilding, "dim_category.parquet")))},
	}
	for _, e := range exports {
		if err := run(e.label, e.query); err != nil {
			return nil, err
		}
	}

	// Metadata the analytics UI shows ("data as of ...").
	meta := []string{
		fmt.Sprintf(`CREATE TABLE dw.etl_info AS
			SELECT now()::TIMESTAMP AS built_at, %d::BIGINT AS max_order_id, %.3f AS build_seconds,
			       (SELECT max(placed_at) FROM dw.fact_orders) AS data_until`,
			res.MaxOrderID, time.Since(began).Seconds()),
		"CREATE TABLE dw.etl_steps (step_no INTEGER, label VARCHAR, row_count BIGINT, seconds DOUBLE)",
	}
	for i, st := range res.Steps {
		meta = append(meta, fmt.Sprintf("INSERT INTO dw.etl_steps VALUES (%d, %s, %d, %.3f)", i+1, quote(st.Label), st.Rows, st.Duration.Seconds()))
	}
	meta = append(meta, "DETACH pg", "CHECKPOINT")
	for _, q := range meta {
		if err := run("", q); err != nil {
			return nil, fmt.Errorf("metadata: %w", err)
		}
	}
	if err := db.Close(); err != nil {
		return nil, err
	}

	// Swap: rename is atomic on the same file system. Processes that still
	// have the old file open keep reading it until they reopen.
	if err := os.Rename(building, final); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(cfg.ParquetDir()); err != nil {
		return nil, err
	}
	if err := os.Rename(parquetBuilding, cfg.ParquetDir()); err != nil {
		return nil, err
	}

	res.Duration = time.Since(began)
	if fi, err := os.Stat(final); err == nil {
		res.SizeBytes = fi.Size()
	}
	return res, nil
}

var createTableRe = regexp.MustCompile(`(?im)^\s*CREATE\s+TABLE\s+([\w.]+)\s+AS\b`)

// quote makes a SQL string literal.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
