package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/irlm/duckstore/internal/bench"
)

type raceState struct {
	mu      sync.Mutex
	running string
	results map[string]*bench.Result
}

func newRaceState() *raceState { return &raceState{results: map[string]*bench.Result{}} }

type raceRow struct {
	Scenario *bench.Scenario
	Result   *bench.Result
	Chart    template.HTML
	Summary  string
}

type raceData struct {
	Groups  []raceGroup
	Running string
}

type raceGroup struct {
	Kind  string
	Title string
	Intro string
	Rows  []raceRow
}

func (s *Server) raceList(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	s.race.mu.Lock()
	defer s.race.mu.Unlock()
	d := raceData{Running: s.race.running}
	groups := []raceGroup{
		{Kind: bench.KindAnalytics, Title: "Analytics: scan, join and aggregate millions of rows",
			Intro: "The questions a dashboard asks. Expect the column store to shine here."},
		{Kind: bench.KindLookup, Title: "Lookups: fetch a few rows by key",
			Intro: "The questions an application asks on every page view."},
		{Kind: bench.KindWrite, Title: "Writes",
			Intro: "Small transactions, concurrency and bulk loads. DuckDB writes go to a separate scratch file, never to the warehouse."},
	}
	for gi := range groups {
		for _, sc := range bench.Scenarios() {
			if sc.Kind != groups[gi].Kind {
				continue
			}
			row := raceRow{Scenario: sc, Result: s.race.results[sc.ID]}
			if row.Result != nil {
				row.Chart, row.Summary = raceChart(row.Result)
			}
			groups[gi].Rows = append(groups[gi].Rows, row)
		}
	}
	d.Groups = groups
	return &view{Template: "race", Title: "Engine race", Data: d}, nil
}

func (s *Server) raceRun(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	sc := bench.Find(r.PathValue("id"))
	if sc == nil {
		return nil, notFound("scenario")
	}
	s.race.mu.Lock()
	if s.race.running != "" {
		running := s.race.running
		s.race.mu.Unlock()
		return &view{Redirect: "/analytics/race", Flash: fmt.Sprintf("%q is still running; timings would disturb each other.", running)}, nil
	}
	s.race.running = sc.Title
	s.race.mu.Unlock()

	// Detach from the request: closing the tab must not leave half-measured results.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()
	res, err := bench.Run(ctx, s.bench, sc)

	s.race.mu.Lock()
	s.race.running = ""
	if err == nil {
		s.race.results[sc.ID] = res
	}
	s.race.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &view{Redirect: "/analytics/race#" + sc.ID}, nil
}

var variantSlot = map[string]int{bench.KeyPostgres: 1, bench.KeyDuckDBRaw: 2, bench.KeyDuckDBStar: 3}

// raceChart draws one bar per variant and a one-line summary.
func raceChart(res *bench.Result) (template.HTML, string) {
	var bars []hbar
	for _, m := range res.Measurements {
		text := formatDuration(m.Median)
		switch {
		case m.Err != "":
			text = "error"
		case m.Ops > 0 && res.Scenario.Kind == bench.KindWrite && m.Median > 0:
			text = fmt.Sprintf("%s · %s ops/s", formatDuration(m.Median), commas(float64(m.Ops)/m.Median.Seconds(), 0))
			if m.Errors > 0 {
				text += fmt.Sprintf(" · %d failed", m.Errors)
			}
		}
		bars = append(bars, hbar{Label: m.Variant.Name(), Value: m.Median.Seconds(), ValueText: text, Slot: variantSlot[m.Variant.Key]})
	}
	summary := ""
	if best := res.Fastest(); best >= 0 {
		b := res.Measurements[best]
		summary = b.Variant.Name() + " was fastest"
		for i, m := range res.Measurements {
			if i != best && m.Err == "" && b.Median > 0 {
				summary += fmt.Sprintf("; %.1f× faster than %s", m.Median.Seconds()/b.Median.Seconds(), m.Variant.Name())
			}
		}
		summary += "."
	}
	return template.HTML(hbarChart(bars, 760, 190)), summary
}
