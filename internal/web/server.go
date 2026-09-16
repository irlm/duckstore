// Package web serves the store UI (Postgres) and the analytics UI (DuckDB).
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/irlm/duckstore/internal/bench"
	"github.com/irlm/duckstore/internal/config"
	"github.com/irlm/duckstore/internal/warehouse"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	cfg   config.Config
	pg    *pgxpool.Pool
	wh    *warehouse.Warehouse
	pages map[string]*template.Template
	etl   *etlRunner
	race  *raceState
	bench bench.Env
	prev  prevStore
}

// Tracer must be passed to pg.Connect so store pages can show their SQL.
func Tracer() pgx.QueryTracer { return pgTracer{} }

func New(cfg config.Config, pool *pgxpool.Pool, wh *warehouse.Warehouse) (*Server, error) {
	s := &Server{
		cfg:   cfg,
		pg:    pool,
		wh:    wh,
		etl:   &etlRunner{},
		race:  newRaceState(),
		bench: bench.Env{PG: pool, WH: wh, DataDir: cfg.DataDir},
	}
	pages, err := fs.Glob(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s.pages = map[string]*template.Template{}
	for _, p := range pages {
		name := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		if name == "layout" {
			continue
		}
		t, err := template.New("").Funcs(templateFuncs).ParseFS(assets, "templates/layout.html", p)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		s.pages[name] = t
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	// Store: Postgres (OLTP)
	mux.Handle("GET /{$}", s.storePage(s.home))
	mux.Handle("GET /c/{slug}", s.storePage(s.category))
	mux.Handle("GET /p/{id}", s.storePage(s.product))
	mux.Handle("POST /p/{id}/price", s.storePage(s.changePrice))
	mux.Handle("GET /search", s.storePage(s.search))
	mux.Handle("GET /cart", s.storePage(s.cart))
	mux.Handle("POST /cart/add", s.storePage(s.cartAdd))
	mux.Handle("POST /cart/update", s.storePage(s.cartUpdate))
	mux.Handle("POST /checkout", s.storePage(s.checkout))
	mux.Handle("GET /orders", s.storePage(s.orders))
	mux.Handle("GET /orders/{id}", s.storePage(s.order))
	mux.Handle("POST /orders/{id}/{action}", s.storePage(s.orderAction))
	mux.Handle("GET /account", s.storePage(s.account))
	mux.Handle("POST /account/switch", s.storePage(s.switchCustomer))

	// Analytics: DuckDB (OLAP)
	mux.Handle("GET /analytics", s.page(s.dashboard))
	mux.Handle("GET /analytics/race", s.page(s.raceList))
	mux.Handle("POST /analytics/race/{id}", s.page(s.raceRun))
	mux.Handle("GET /analytics/sql", s.page(s.console))
	mux.Handle("POST /analytics/sql", s.page(s.console))
	mux.Handle("GET /analytics/etl", s.page(s.etlPage))
	mux.Handle("POST /analytics/etl", s.page(s.etlStart))

	return s.middleware(mux)
}

// middleware adds the per-request query log, logs requests and recovers panics.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx, _ := withQueryLog(r.Context())
		defer func() {
			if p := recover(); p != nil {
				log.Printf("panic: %v\n%s", p, debug.Stack())
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			log.Printf("%s %s %s", r.Method, r.URL.RequestURI(), time.Since(start).Round(time.Millisecond))
		}
	})
}

type pageData struct {
	Title    string
	Section  string // "store" or "analytics"
	Path     string
	Customer *Customer
	Flash    string
	Queries  []QueryEntry
	Data     any

	PrevAction  string // queries of the POST that redirected here
	PrevQueries []QueryEntry
}

// view is what a handler returns: a template name and its data, or a redirect.
type view struct {
	Template string
	Title    string
	Data     any
	Redirect string
	Flash    string
}

type handler func(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error)

// storePage loads the current customer (cookie) before the handler runs.
func (s *Server) storePage(h handler) http.Handler {
	return s.wrap("store", h, true)
}

func (s *Server) page(h handler) http.Handler {
	return s.wrap("analytics", h, false)
}

func (s *Server) wrap(section string, h handler, needCustomer bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := &pageData{Section: section, Path: r.URL.Path, Flash: readFlash(w, r)}
		if needCustomer {
			c, err := s.currentCustomer(w, r)
			if err != nil {
				s.renderError(w, r, p, err)
				return
			}
			p.Customer = c
		}
		v, err := h(w, r, p)
		if err != nil {
			s.renderError(w, r, p, err)
			return
		}
		if v == nil {
			return // handler wrote the response itself
		}
		if v.Redirect != "" {
			if v.Flash != "" {
				setFlash(w, v.Flash)
			}
			http.Redirect(w, r, v.Redirect, http.StatusSeeOther)
			return
		}
		p.Title, p.Data = v.Title, v.Data
		s.render(w, r, v.Template, http.StatusOK, p)
	})
}

type notFound string

func (e notFound) Error() string { return string(e) + " not found" }

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, p *pageData, err error) {
	status := http.StatusInternalServerError
	var nf notFound
	switch {
	case errors.As(err, &nf) || errors.Is(err, pgx.ErrNoRows):
		status = http.StatusNotFound
	case errors.Is(err, warehouse.ErrNotBuilt):
		status = http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled):
		return
	}
	log.Printf("error %s %s: %v", r.Method, r.URL.Path, err)
	p.Title = http.StatusText(status)
	p.Data = err.Error()
	s.render(w, r, "error", status, p)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, status int, p *pageData) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "unknown template "+name, http.StatusInternalServerError)
		return
	}
	p.Queries = queryLogFrom(r.Context()).snapshot()
	p.PrevAction, p.PrevQueries = s.takePrevQueries(w, r)
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", p); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func setFlash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: "flash", Value: url.QueryEscape(msg), Path: "/", MaxAge: 60, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func readFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie("flash")
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: "flash", Path: "/", MaxAge: -1})
	msg, _ := url.QueryUnescape(c.Value)
	return msg
}
