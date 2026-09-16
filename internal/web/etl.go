package web

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/irlm/duckstore/internal/query"
	"github.com/irlm/duckstore/internal/warehouse"
)

type etlRunner struct {
	mu       sync.Mutex
	running  bool
	started  time.Time
	finished time.Time
	steps    []warehouse.Step
	err      string
	result   *warehouse.BuildResult
}

type etlData struct {
	Running  bool
	Started  time.Time
	Finished time.Time
	Steps    []warehouse.Step
	Err      string
	Result   *warehouse.BuildResult
	Last     *query.Result // dw.etl_steps of the warehouse in use
	LastErr  string
	Info     map[string]any
}

func (s *Server) etlPage(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	s.etl.mu.Lock()
	d := etlData{
		Running: s.etl.running, Started: s.etl.started, Finished: s.etl.finished,
		Steps: append([]warehouse.Step(nil), s.etl.steps...), Err: s.etl.err, Result: s.etl.result,
	}
	s.etl.mu.Unlock()
	if !d.Running {
		ctx := r.Context()
		if info, err := duck(ctx, s.wh, sqlETLInfo, 1); err == nil {
			d.Info = rowMap(info)
			d.Last, err = duck(ctx, s.wh, `-- steps of the build currently in use
SELECT step_no, label, row_count, seconds FROM dw.etl_steps ORDER BY step_no`, 0)
			if err != nil {
				d.LastErr = err.Error()
			}
		} else if !errors.Is(err, warehouse.ErrNotBuilt) {
			d.LastErr = err.Error()
		}
	}
	return &view{Template: "etl", Title: "ETL", Data: d}, nil
}

func (s *Server) etlStart(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	s.etl.mu.Lock()
	if s.etl.running {
		s.etl.mu.Unlock()
		return &view{Redirect: "/analytics/etl", Flash: "The ETL is already running."}, nil
	}
	s.etl.running, s.etl.started, s.etl.steps, s.etl.err, s.etl.result = true, time.Now(), nil, "", nil
	s.etl.mu.Unlock()

	go func() {
		ctx := context.Background()
		res, err := warehouse.Build(ctx, s.cfg, func(st warehouse.Step) {
			s.etl.mu.Lock()
			s.etl.steps = append(s.etl.steps, st)
			s.etl.mu.Unlock()
		})
		if err == nil {
			err = s.wh.Reload(ctx)
		}
		s.etl.mu.Lock()
		defer s.etl.mu.Unlock()
		s.etl.running, s.etl.finished, s.etl.result = false, time.Now(), res
		if err != nil {
			s.etl.err = err.Error()
		}
	}()
	return &view{Redirect: "/analytics/etl"}, nil
}
