// Package query runs a SQL statement on DuckDB or Postgres and returns the
// rows in one common shape, with timing. The web UI, the SQL console, the
// benchmark and the CLI all use it.
package query

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/duckdb/duckdb-go/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	EngineDuckDB   = "duckdb"
	EnginePostgres = "postgres"
)

type Result struct {
	Engine    string
	SQL       string
	Columns   []string
	Rows      [][]any
	RowCount  int // rows read (can be more than len(Rows) when truncated)
	Truncated bool
	Duration  time.Duration // until the last row was read
}

// DuckDB runs a query on a DuckDB database. maxRows <= 0 keeps every row.
func DuckDB(ctx context.Context, db *sql.DB, q string, maxRows int, args ...any) (*Result, error) {
	start := time.Now()
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &Result{Engine: EngineDuckDB, SQL: q, Columns: cols}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		res.add(vals, maxRows)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.Duration = time.Since(start)
	return res, nil
}

// Querier is satisfied by *pgxpool.Pool, *pgx.Conn and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Postgres runs a query on Postgres. maxRows <= 0 keeps every row.
func Postgres(ctx context.Context, db Querier, q string, maxRows int, args ...any) (*Result, error) {
	start := time.Now()
	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res := &Result{Engine: EnginePostgres, SQL: q}
	for _, f := range rows.FieldDescriptions() {
		res.Columns = append(res.Columns, f.Name)
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		res.add(vals, maxRows)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.Duration = time.Since(start)
	return res, nil
}

// PostgresReadOnly runs a query inside a READ ONLY transaction with a
// statement timeout, then rolls back. Used for user-typed SQL.
func PostgresReadOnly(ctx context.Context, pool *pgxpool.Pool, q string, maxRows int, timeout time.Duration) (*Result, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
		return nil, err
	}
	return Postgres(ctx, tx, q, maxRows)
}

func (r *Result) add(vals []any, maxRows int) {
	r.RowCount++
	if maxRows > 0 && len(r.Rows) >= maxRows {
		r.Truncated = true
		return
	}
	r.Rows = append(r.Rows, vals)
}

// Float returns a numeric cell as float64 (for charts), or false.
func Float(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f, true
	case duckdb.Decimal:
		return x.Float64(), true
	case pgtype.Numeric:
		f, err := x.Float64Value()
		return f.Float64, err == nil && f.Valid
	}
	return 0, false
}

// Format turns a cell into display text.
func Format(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return x
	case []byte:
		return string(x)
	case bool:
		return fmt.Sprint(x)
	case time.Time:
		switch {
		case x.Hour() == 0 && x.Minute() == 0 && x.Second() == 0 && x.Nanosecond() == 0:
			return x.Format(time.DateOnly)
		case x.Nanosecond() == 0:
			return x.Format(time.DateTime)
		default:
			return x.Format("2006-01-02 15:04:05.000")
		}
	case float32, float64:
		f, _ := Float(x)
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", f), "0"), ".")
	case duckdb.Decimal:
		return decimalString(x.Value, int(x.Scale))
	case pgtype.Numeric:
		if !x.Valid {
			return "NULL"
		}
		if x.NaN {
			return "NaN"
		}
		if x.Exp < 0 {
			return decimalString(x.Int, int(-x.Exp))
		}
		return new(big.Int).Mul(x.Int, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(x.Exp)), nil)).String()
	case duckdb.Interval:
		return fmt.Sprintf("%d months %d days %s", x.Months, x.Days, time.Duration(x.Micros)*time.Microsecond)
	case pgtype.Interval:
		return fmt.Sprintf("%d months %d days %s", x.Months, x.Days, time.Duration(x.Microseconds)*time.Microsecond)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = quoteNested(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		parts := make([]string, 0, len(x))
		for k, e := range x {
			parts = append(parts, k+": "+quoteNested(e))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case duckdb.Map:
		parts := make([]string, 0, len(x))
		for k, e := range x {
			parts = append(parts, quoteNested(k)+"="+quoteNested(e))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

func quoteNested(v any) string {
	if s, ok := v.(string); ok {
		return "'" + s + "'"
	}
	return Format(v)
}

func decimalString(i *big.Int, scale int) string {
	if i == nil {
		return "NULL"
	}
	neg := i.Sign() < 0
	s := new(big.Int).Abs(i).String()
	if scale > 0 {
		if len(s) <= scale {
			s = strings.Repeat("0", scale-len(s)+1) + s
		}
		s = s[:len(s)-scale] + "." + s[len(s)-scale:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
