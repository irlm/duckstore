package web

import (
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"

	"github.com/irlm/duckstore/internal/query"
)

var templateFuncs = template.FuncMap{
	"money":   formatMoney,
	"usd":     func(v float64) string { return "$" + commas(v, 2) },
	"compact": compactUSD,
	"num":     func(v any) string { f, _ := query.Float(v); return commas(f, 0) },
	"num1":    func(v float64) string { return commas(v, 1) },
	"dec2":    func(v float64) string { return commas(v, 2) },
	"plural": func(n any, word string) string {
		f, _ := query.Float(n)
		if f == 1 {
			return "1 " + word
		}
		return commas(f, 0) + " " + word + "s"
	},
	"pct":  func(v float64) string { return fmt.Sprintf("%.1f%%", v) },
	"dur":  formatDuration,
	"date": func(t time.Time) string { return t.UTC().Format("Jan 2, 2006") },
	"datetime": func(t time.Time) string {
		return t.UTC().Format("Jan 2, 2006 15:04 UTC")
	},
	"stars": func(r float64) string {
		n := int(math.Round(r))
		return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
	},
	"cell":   query.Format,
	"isNum":  func(v any) bool { _, ok := query.Float(v); return ok },
	"add":    func(a, b int) int { return a + b },
	"sub":    func(a, b int) int { return a - b },
	"engine": engineName,
	"flt":    func(v any) float64 { f, _ := query.Float(v); return f },
	"float":  func(v any) float64 { f, _ := query.Float(v); return f },
	"int64":  func(v any) int64 { f, _ := query.Float(v); return int64(f) },
	"mulf": func(a float64, b any) float64 {
		f, _ := query.Float(b)
		return a * f
	},
	"pctOf": func(part, whole int64) float64 {
		if whole == 0 {
			return 0
		}
		return float64(part) / float64(whole) * 100
	},
	"mb":        func(b int64) float64 { return float64(b) / 1e6 },
	"slotClass": func(key string) string { return fmt.Sprintf("s%d", variantSlot[key]) },
	"plist": func(c *Customer, items []productCard, showUnits bool) productListView {
		return productListView{Customer: c, Items: items, ShowUnits: showUnits}
	},
	"safe":      func(s string) template.HTML { return template.HTML(s) },
	"lower":     strings.ToLower,
	"hasPrefix": strings.HasPrefix,
	"sumDur": func(qs []QueryEntry, engine string) time.Duration {
		var d time.Duration
		for _, q := range qs {
			if q.Engine == engine {
				d += q.Duration
			}
		}
		return d
	},
	"countEngine": func(qs []QueryEntry, engine string) int {
		n := 0
		for _, q := range qs {
			if q.Engine == engine {
				n++
			}
		}
		return n
	},
}

func engineName(e string) string {
	switch e {
	case "postgres":
		return "Postgres"
	case "duckdb":
		return "DuckDB"
	}
	return e
}

// commas formats 1234567.891 as "1,234,567.89".
func commas(v float64, decimals int) string {
	neg := v < 0
	s := fmt.Sprintf("%.*f", decimals, math.Abs(v))
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

func formatMoney(c *Customer, usd float64) string {
	if c == nil {
		return "$" + commas(usd, 2)
	}
	local := usd * c.FX
	decimals := 2
	if c.Currency == "JPY" || c.Currency == "CLP" {
		decimals = 0
	}
	return c.Symbol + commas(local, decimals)
}

func compactUSD(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e9:
		return fmt.Sprintf("$%.1fB", v/1e9)
	case a >= 1e6:
		return fmt.Sprintf("$%.1fM", v/1e6)
	case a >= 1e3:
		return fmt.Sprintf("$%.1fK", v/1e3)
	}
	return fmt.Sprintf("$%.0f", v)
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%.2f ms", float64(d)/float64(time.Millisecond))
	case d < 100*time.Millisecond:
		return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond))
	case d < time.Second:
		return fmt.Sprintf("%.0f ms", float64(d)/float64(time.Millisecond))
	}
	return fmt.Sprintf("%.2f s", d.Seconds())
}

type productListView struct {
	Customer  *Customer
	Items     []productCard
	ShowUnits bool
}
