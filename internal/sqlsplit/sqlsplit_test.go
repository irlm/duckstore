package sqlsplit

import (
	"slices"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{"simple", "SELECT 1; SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"no trailing semicolon", "SELECT 1;\nSELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"semicolon in string", "SELECT 'a;b'; SELECT 'it''s; fine'", []string{"SELECT 'a;b'", "SELECT 'it''s; fine'"}},
		{"semicolon in identifier", `SELECT 1 AS "x;y"; SELECT 2`, []string{`SELECT 1 AS "x;y"`, "SELECT 2"}},
		{"line comment", "-- first; comment\nSELECT 1; -- trailing; comment\n", []string{"-- first; comment\nSELECT 1"}},
		{"block comment", "/* a; b */ SELECT 1; SELECT /* ; */ 2", []string{"/* a; b */ SELECT 1", "SELECT /* ; */ 2"}},
		{"dollar quoted", "DO $$ BEGIN PERFORM 1; END $$; SELECT 2", []string{"DO $$ BEGIN PERFORM 1; END $$", "SELECT 2"}},
		{"tagged dollar quote", "SELECT $fn$ ; $fn$; SELECT $1", []string{"SELECT $fn$ ; $fn$", "SELECT $1"}},
		{"only comments", "-- nothing here;\n/* nor here */;", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, s := range Split(tt.script) {
				got = append(got, s.SQL)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Split(%q)\n got: %q\nwant: %q", tt.script, got, tt.want)
			}
		})
	}
}

func TestSplitLabels(t *testing.T) {
	stmts := Split("-- step: load orders\n-- more text\nCREATE TABLE a AS SELECT 1;\nSELECT 2;")
	if len(stmts) != 2 || stmts[0].Label != "load orders" || stmts[1].Label != "" {
		t.Fatalf("unexpected labels: %+v", stmts)
	}
}
