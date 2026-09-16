package lessons

import (
	"strings"
	"testing"

	"github.com/irlm/duckstore/internal/sqlsplit"
)

func TestLessonsHaveHeaders(t *testing.T) {
	list := List()
	if len(list) < 10 {
		t.Fatalf("expected at least 10 lessons, got %d", len(list))
	}
	for _, l := range list {
		if l.Title == l.Name {
			t.Errorf("%s: missing '-- title:' header", l.Name)
		}
		if l.Engine != "duckdb" && l.Engine != "postgres" {
			t.Errorf("%s: engine %q must be duckdb or postgres", l.Name, l.Engine)
		}
		if !strings.HasPrefix(l.Title, l.Name[:2]) {
			t.Errorf("%s: title %q should start with the lesson number", l.Name, l.Title)
		}
		if len(sqlsplit.Split(l.SQL)) == 0 {
			t.Errorf("%s: no statements", l.Name)
		}
	}
}

func TestGetRejectsPaths(t *testing.T) {
	if _, ok := Get("../go.mod"); ok {
		t.Fatal("Get must not read outside the lessons")
	}
}
