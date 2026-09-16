package web

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/http"
	"strings"
	"time"

	"github.com/irlm/duckstore/internal/query"
	"github.com/irlm/duckstore/internal/sqlsplit"
	"github.com/irlm/duckstore/lessons"
)

// The SQL console runs any SQL on DuckDB (warehouse + live Postgres attached
// as "pg") or on Postgres (read-only transaction). Lessons from lessons/*.sql
// can be loaded into it.

const consoleMaxRows = 500

type consoleResult struct {
	SQL    string
	Result *query.Result
	Err    string
	Plan   string // EXPLAIN output shown as text
}

type consoleData struct {
	Engine  string
	SQL     string
	Explain bool
	Lesson  string
	Lessons []lessons.Lesson
	Results []consoleResult
	Total   time.Duration
}

const consoleWelcome = `-- Welcome to the SQL console.
-- DuckDB engine: the warehouse (schemas dw and raw) plus the live store as "pg".
-- Postgres engine: the store (schema store), read-only.
-- Load a lesson from the list, or try:

SELECT year(order_date) AS year, count(*) AS orders, sum(total_usd) AS revenue_usd
FROM dw.fact_orders
GROUP BY ALL
ORDER BY ALL;

-- DuckDB can describe any table or query:
SUMMARIZE dw.fact_orders;`

func (s *Server) console(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	d := consoleData{Engine: r.FormValue("engine"), SQL: r.FormValue("sql"), Explain: r.FormValue("explain") == "on", Lessons: lessons.List()}
	if d.Engine != query.EnginePostgres {
		d.Engine = query.EngineDuckDB
	}
	if name := r.URL.Query().Get("lesson"); name != "" && r.Method == http.MethodGet {
		l, ok := lessons.Get(name)
		if !ok {
			return nil, notFound("lesson")
		}
		d.Lesson, d.SQL, d.Engine = l.Name, l.SQL, l.Engine
	}
	if d.SQL == "" {
		d.SQL = consoleWelcome
	}
	if r.Method == http.MethodPost {
		start := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		var err error
		if d.Engine == query.EnginePostgres {
			d.Results, err = s.consolePostgres(ctx, d.SQL, d.Explain)
		} else {
			d.Results, err = s.consoleDuckDB(ctx, d.SQL, d.Explain)
		}
		if err != nil {
			return nil, err
		}
		d.Total = time.Since(start)
	}
	return &view{Template: "sql", Title: "SQL console", Data: d}, nil
}

func (s *Server) consoleDuckDB(ctx context.Context, script string, explain bool) ([]consoleResult, error) {
	var out []consoleResult
	err := s.wh.With(func(db *sql.DB) error {
		// A dedicated connection, so SET/USE in the script apply to the next
		// statements. It is thrown away afterwards so they never leak into
		// the pool that the dashboard uses.
		conn, err := db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		defer conn.Raw(func(any) error { return driver.ErrBadConn })

		for _, st := range sqlsplit.Split(script) {
			q := st.SQL
			if explain {
				q = "EXPLAIN ANALYZE " + q
			}
			cr := consoleResult{SQL: st.SQL}
			res, err := runOnConn(ctx, conn, q)
			queryLogFrom(ctx).add(QueryEntry{Engine: query.EngineDuckDB, SQL: q, Duration: durationOf(res), Rows: rowsOf(res), Err: errText(err)})
			if err != nil {
				cr.Err = err.Error()
				out = append(out, cr)
				break
			}
			cr.Result, cr.Plan = res, planText(res)
			out = append(out, cr)
		}
		return nil
	})
	return out, err
}

func runOnConn(ctx context.Context, conn *sql.Conn, q string) (*query.Result, error) {
	start := time.Now()
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res := &query.Result{Engine: query.EngineDuckDB, SQL: q, Columns: cols}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		res.RowCount++
		if len(res.Rows) < consoleMaxRows {
			res.Rows = append(res.Rows, vals)
		} else {
			res.Truncated = true
		}
	}
	res.Duration = time.Since(start)
	return res, rows.Err()
}

func (s *Server) consolePostgres(ctx context.Context, script string, explain bool) ([]consoleResult, error) {
	var out []consoleResult
	for _, st := range sqlsplit.Split(script) {
		q := st.SQL
		if explain {
			q = "EXPLAIN (ANALYZE, BUFFERS) " + q
		}
		cr := consoleResult{SQL: st.SQL}
		res, err := query.PostgresReadOnly(ctx, s.pg, q, consoleMaxRows, 5*time.Minute)
		if err != nil {
			cr.Err = err.Error()
			out = append(out, cr)
			break
		}
		cr.Result, cr.Plan = res, planText(res)
		out = append(out, cr)
	}
	return out, nil
}

// planText joins EXPLAIN output into one text block.
func planText(res *query.Result) string {
	if res == nil || len(res.Columns) == 0 {
		return ""
	}
	col := -1
	for i, c := range res.Columns {
		if c == "QUERY PLAN" || c == "explain_value" {
			col = i
		}
	}
	if col < 0 {
		return ""
	}
	var b strings.Builder
	for _, row := range res.Rows {
		b.WriteString(query.Format(row[col]))
		b.WriteByte('\n')
	}
	return b.String()
}

func durationOf(r *query.Result) time.Duration {
	if r == nil {
		return 0
	}
	return r.Duration
}

func rowsOf(r *query.Result) int64 {
	if r == nil {
		return 0
	}
	return int64(r.RowCount)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
