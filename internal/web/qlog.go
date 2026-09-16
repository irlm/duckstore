package web

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/irlm/duckstore/internal/query"
	"github.com/irlm/duckstore/internal/warehouse"
)

// Every page shows the SQL it ran. Postgres queries are captured by a pgx
// tracer (so even BEGIN/COMMIT inside transactions appear); DuckDB queries go
// through Server.duck.

type QueryEntry struct {
	Engine   string
	Title    string // first "-- " comment line of the SQL, if any
	SQL      string
	Args     string
	Duration time.Duration
	Rows     int64
	Err      string
}

type queryLog struct {
	mu      sync.Mutex
	entries []QueryEntry
}

type queryLogKey struct{}

func withQueryLog(ctx context.Context) (context.Context, *queryLog) {
	l := &queryLog{}
	return context.WithValue(ctx, queryLogKey{}, l), l
}

func queryLogFrom(ctx context.Context) *queryLog {
	l, _ := ctx.Value(queryLogKey{}).(*queryLog)
	return l
}

func (l *queryLog) add(e QueryEntry) {
	if l == nil {
		return
	}
	e.SQL = strings.TrimSpace(e.SQL)
	for line := range strings.Lines(e.SQL) {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "-- "); ok {
			e.Title = t
		}
		break
	}
	l.mu.Lock()
	l.entries = append(l.entries, e)
	l.mu.Unlock()
}

func (l *queryLog) snapshot() []QueryEntry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]QueryEntry(nil), l.entries...)
}

func formatArgs(args []any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, a := range args {
		s := query.Format(a)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		parts[i] = fmt.Sprintf("$%d = %s", i+1, s)
	}
	return strings.Join(parts, ", ")
}

// pgTracer implements pgx.QueryTracer.
type pgTracer struct{}

type traceStartKey struct{}

type traceStart struct {
	sql  string
	args []any
	at   time.Time
}

func (pgTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if queryLogFrom(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, traceStartKey{}, &traceStart{d.SQL, d.Args, time.Now()})
}

func (pgTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	st, _ := ctx.Value(traceStartKey{}).(*traceStart)
	l := queryLogFrom(ctx)
	if st == nil || l == nil {
		return
	}
	e := QueryEntry{Engine: query.EnginePostgres, SQL: st.sql, Args: formatArgs(st.args), Duration: time.Since(st.at), Rows: d.CommandTag.RowsAffected()}
	if d.Err != nil {
		e.Err = d.Err.Error()
	}
	l.add(e)
}

// duck runs a query on the DuckDB warehouse and records it.
func duck(ctx context.Context, wh *warehouse.Warehouse, q string, maxRows int, args ...any) (*query.Result, error) {
	var res *query.Result
	start := time.Now()
	err := wh.With(func(db *sql.DB) error {
		var err error
		res, err = query.DuckDB(ctx, db, q, maxRows, args...)
		return err
	})
	e := QueryEntry{Engine: query.EngineDuckDB, SQL: q, Args: formatArgs(args), Duration: time.Since(start)}
	if res != nil {
		e.Rows = int64(res.RowCount)
	}
	if err != nil {
		e.Err = err.Error()
	}
	queryLogFrom(ctx).add(e)
	return res, err
}

// A POST that redirects would lose its query log (a checkout transaction is
// the most interesting one), so it is kept for the next page to show.
type prevStore struct {
	mu sync.Mutex
	m  map[string]prevEntry
}

type prevEntry struct {
	action  string
	entries []QueryEntry
	at      time.Time
}

func (p *prevStore) put(action string, entries []QueryEntry) string {
	b := make([]byte, 12)
	rand.Read(b)
	token := hex.EncodeToString(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]prevEntry{}
	}
	for k, v := range p.m {
		if time.Since(v.at) > 5*time.Minute {
			delete(p.m, k)
		}
	}
	p.m[token] = prevEntry{action, entries, time.Now()}
	return token
}

func (p *prevStore) take(token string) (string, []QueryEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.m[token]
	if !ok {
		return "", nil
	}
	delete(p.m, token)
	return e.action, e.entries
}

// keepQueries saves this request's queries for the page after the redirect.
func (s *Server) keepQueries(w http.ResponseWriter, r *http.Request, action string) {
	entries := queryLogFrom(r.Context()).snapshot()
	if len(entries) == 0 {
		return
	}
	token := s.prev.put(action, entries)
	http.SetCookie(w, &http.Cookie{Name: "prevq", Value: token, Path: "/", MaxAge: 300, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) takePrevQueries(w http.ResponseWriter, r *http.Request) (string, []QueryEntry) {
	c, err := r.Cookie("prevq")
	if err != nil {
		return "", nil
	}
	http.SetCookie(w, &http.Cookie{Name: "prevq", Path: "/", MaxAge: -1})
	return s.prev.take(c.Value)
}
