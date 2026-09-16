// Package sqlsplit splits a SQL script into statements, like the "GO"
// batches in SSMS, but on semicolons. It understands quotes, comments and
// Postgres dollar-quoted strings, so a ';' inside them does not split.
package sqlsplit

import "strings"

type Statement struct {
	SQL   string // statement text without the trailing semicolon
	Label string // text of a "-- step: ..." comment before the statement, if any
}

// Split returns the statements in script. Pieces that contain only comments
// or whitespace are dropped.
func Split(script string) []Statement {
	var out []Statement
	start := 0
	i := 0
	n := len(script)
	for i < n {
		c := script[i]
		switch {
		case c == '\'' || c == '"':
			i = skipQuoted(script, i, c)
		case c == '-' && i+1 < n && script[i+1] == '-':
			for i < n && script[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && script[i+1] == '*':
			end := strings.Index(script[i+2:], "*/")
			if end < 0 {
				i = n
			} else {
				i += end + 4
			}
		case c == '$':
			i = skipDollarQuoted(script, i)
		case c == ';':
			out = appendStatement(out, script[start:i])
			i++
			start = i
		default:
			i++
		}
	}
	return appendStatement(out, script[start:])
}

func skipQuoted(s string, i int, quote byte) int {
	i++
	for i < len(s) {
		if s[i] == quote {
			if i+1 < len(s) && s[i+1] == quote { // escaped '' or ""
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}

// skipDollarQuoted handles $$...$$ and $tag$...$tag$. A '$' that does not
// start a valid tag (for example $1 parameters) is skipped as one character.
func skipDollarQuoted(s string, i int) int {
	j := i + 1
	for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || j > i+1 && s[j] >= '0' && s[j] <= '9') {
		j++
	}
	if j >= len(s) || s[j] != '$' {
		return i + 1
	}
	tag := s[i : j+1]
	end := strings.Index(s[j+1:], tag)
	if end < 0 {
		return len(s)
	}
	return j + 1 + end + len(tag)
}

func appendStatement(out []Statement, piece string) []Statement {
	label := ""
	hasCode := false
	for line := range strings.Lines(piece) {
		t := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(t, "-- step:"); ok && !hasCode {
			label = strings.TrimSpace(after)
		}
		if t != "" && !strings.HasPrefix(t, "--") {
			hasCode = true
		}
	}
	if !hasCode || strings.TrimSpace(stripComments(piece)) == "" {
		return out
	}
	return append(out, Statement{SQL: strings.TrimSpace(piece), Label: label})
}

// stripComments removes -- and /* */ comments (outside quotes).
func stripComments(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		switch {
		case s[i] == '\'' || s[i] == '"':
			j := skipQuoted(s, i, s[i])
			b.WriteString(s[i:j])
			i = j
		case strings.HasPrefix(s[i:], "--"):
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case strings.HasPrefix(s[i:], "/*"):
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 4
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}
