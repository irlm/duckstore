// Package lessons embeds the SQL lessons so the web console can load them.
// Each file starts with header comments:
//
//	-- title: Friendly SQL
//	-- engine: duckdb
package lessons

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed *.sql
var files embed.FS

type Lesson struct {
	Name   string // file name without .sql, e.g. "01-friendly-sql"
	Title  string
	Engine string // "duckdb" or "postgres"
	SQL    string
}

func List() []Lesson {
	names, _ := fs.Glob(files, "*.sql")
	out := make([]Lesson, 0, len(names))
	for _, n := range names {
		if l, ok := Get(strings.TrimSuffix(n, ".sql")); ok {
			out = append(out, l)
		}
	}
	return out
}

func Get(name string) (Lesson, bool) {
	if strings.ContainsAny(name, "/\\") {
		return Lesson{}, false
	}
	b, err := files.ReadFile(name + ".sql")
	if err != nil {
		return Lesson{}, false
	}
	l := Lesson{Name: name, Title: name, Engine: "duckdb", SQL: string(b)}
	for line := range strings.Lines(l.SQL) {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "--") {
			break
		}
		if v, ok := strings.CutPrefix(t, "-- title:"); ok {
			l.Title = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(t, "-- engine:"); ok {
			l.Engine = strings.TrimSpace(v)
		}
	}
	return l, true
}
