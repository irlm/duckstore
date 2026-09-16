package web

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"
)

// Server-side SVG charts. Colors come from CSS custom properties (see
// static/style.css), so light and dark mode are handled by the stylesheet.
// Every mark carries data-tip-label / data-tip-value for the hover tooltip
// (static/app.js) and is focusable, so keyboard users get the same readout.

const (
	chartFont   = 12
	axisBand    = 22 // room under the plot for x labels
	leftAxis    = 56
	barMaxThick = 24
)

func esc(s string) string { return html.EscapeString(s) }

// niceTicks returns about n round tick values from 0 to >= maxV.
func niceTicks(maxV float64, n int) []float64 {
	if maxV <= 0 {
		return []float64{0, 1}
	}
	raw := maxV / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := mag * 10
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if raw <= m*mag {
			step = m * mag
			break
		}
	}
	var ticks []float64
	for v := 0.0; v < maxV+step*0.999; v += step {
		ticks = append(ticks, v)
	}
	return ticks
}

// roundedTop draws a column with a 4px rounded top and a square base.
func roundedTop(x, y, w, h float64) string {
	r := math.Min(4, math.Min(w/2, h))
	return fmt.Sprintf("M%.1f,%.1f V%.1f Q%.1f,%.1f %.1f,%.1f H%.1f Q%.1f,%.1f %.1f,%.1f V%.1f Z",
		x, y+h, y+r, x, y, x+r, y, x+w-r, x+w, y, x+w, y+r, y+h)
}

// roundedRight draws a horizontal bar with a 4px rounded end and a square base.
func roundedRight(x, y, w, h float64) string {
	r := math.Min(4, math.Min(h/2, w))
	return fmt.Sprintf("M%.1f,%.1f H%.1f Q%.1f,%.1f %.1f,%.1f V%.1f Q%.1f,%.1f %.1f,%.1f H%.1f Z",
		x, y, x+w-r, x+w, y, x+w, y+r, y+h-r, x+w, y+h, x+w-r, y+h, x)
}

type column struct {
	Label     string // x-axis label (may be empty to skip)
	TipLabel  string
	Value     float64
	ValueText string
}

// columnChart: one series over time (magnitude), color slot 1.
func columnChart(cols []column, width, height int, axisFmt func(float64) string, labelLast bool) string {
	if len(cols) == 0 {
		return ""
	}
	maxV := 0.0
	for _, c := range cols {
		maxV = math.Max(maxV, c.Value)
	}
	ticks := niceTicks(maxV, 4)
	top := ticks[len(ticks)-1]
	plotW := float64(width - leftAxis - 8)
	plotH := float64(height - axisBand - 16)
	y0 := 16.0
	slot := plotW / float64(len(cols))
	thick := math.Min(barMaxThick, slot-2) // 2px surface gap between touching bars
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %d %d" style="max-width: %dpx" role="img" aria-label="column chart">`, width, height, width)
	for _, t := range ticks {
		y := y0 + plotH - t/top*plotH
		fmt.Fprintf(&b, `<line class="grid" x1="%d" x2="%d" y1="%.1f" y2="%.1f"/>`, leftAxis, width-8, y, y)
		fmt.Fprintf(&b, `<text class="tick" x="%d" y="%.1f" text-anchor="end" dominant-baseline="middle">%s</text>`, leftAxis-6, y, esc(axisFmt(t)))
	}
	for i, c := range cols {
		h := c.Value / top * plotH
		x := float64(leftAxis) + float64(i)*slot + (slot-thick)/2
		y := y0 + plotH - h
		fmt.Fprintf(&b, `<g class="mark" tabindex="0" data-tip-label="%s" data-tip-value="%s">`, esc(c.TipLabel), esc(c.ValueText))
		fmt.Fprintf(&b, `<rect class="hit" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, float64(leftAxis)+float64(i)*slot, y0, slot, plotH)
		if h > 0.5 {
			fmt.Fprintf(&b, `<path class="s1" d="%s"/>`, roundedTop(x, y, thick, h))
		}
		b.WriteString(`</g>`)
		if c.Label != "" {
			fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x+thick/2, y0+plotH+16, esc(c.Label))
		}
		if labelLast && i == len(cols)-1 {
			fmt.Fprintf(&b, `<text class="value" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, x+thick, y-6, esc(c.ValueText))
		}
	}
	fmt.Fprintf(&b, `<line class="baseline" x1="%d" x2="%d" y1="%.1f" y2="%.1f"/>`, leftAxis, width-8, y0+plotH, y0+plotH)
	b.WriteString(`</svg>`)
	return b.String()
}

type hbar struct {
	Label     string
	Value     float64
	ValueText string
	Slot      int // color slot 1..3
}

// hbarChart: horizontal bars with the label on the left and the value at the tip.
func hbarChart(bars []hbar, width, labelWidth int) string {
	if len(bars) == 0 {
		return ""
	}
	const thick, gap = 18.0, 12.0
	maxV := 0.0
	for _, h := range bars {
		maxV = math.Max(maxV, h.Value)
	}
	if maxV == 0 {
		maxV = 1
	}
	valueRoom := 110.0
	plotW := float64(width-labelWidth) - valueRoom
	height := int(float64(len(bars))*(thick+gap) + gap)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %d %d" style="max-width: %dpx" role="img" aria-label="bar chart">`, width, height, width)
	for i, h := range bars {
		y := gap/2 + float64(i)*(thick+gap)
		w := math.Max(h.Value/maxV*plotW, 1)
		slot := max(h.Slot, 1)
		label := h.Label
		if len([]rune(label)) > labelWidth/7 {
			label = string([]rune(label)[:labelWidth/7-1]) + "…"
		}
		fmt.Fprintf(&b, `<g class="mark" tabindex="0" data-tip-label="%s" data-tip-value="%s">`, esc(h.Label), esc(h.ValueText))
		fmt.Fprintf(&b, `<rect class="hit" x="0" y="%.1f" width="%d" height="%.1f"/>`, y-gap/2, width, thick+gap)
		fmt.Fprintf(&b, `<text class="label" x="%d" y="%.1f" text-anchor="end" dominant-baseline="middle">%s</text>`, labelWidth-8, y+thick/2, esc(label))
		fmt.Fprintf(&b, `<path class="s%d" d="%s"/>`, slot, roundedRight(float64(labelWidth), y, w, thick))
		fmt.Fprintf(&b, `<text class="value" x="%.1f" y="%.1f" dominant-baseline="middle">%s</text>`, float64(labelWidth)+w+6, y+thick/2, esc(h.ValueText))
		b.WriteString(`</g>`)
	}
	fmt.Fprintf(&b, `<line class="baseline" x1="%d" x2="%d" y1="0" y2="%d"/>`, labelWidth, labelWidth, height)
	b.WriteString(`</svg>`)
	return b.String()
}

// heatmap: sequential one-hue ramp (7 steps). NaN cells are left empty.
func heatmap(rows, cols []string, values [][]float64, cellText func(float64) string, tip func(r, c int, v float64) string, minV, maxV float64) string {
	const cellW, cellH, gap = 44.0, 26.0, 2.0
	labelW := 90.0
	width := int(labelW + float64(len(cols))*(cellW+gap))
	height := int(20 + float64(len(rows))*(cellH+gap))
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart heatmap" viewBox="0 0 %d %d" style="max-width: %dpx" role="img" aria-label="heatmap">`, width, height, width)
	for j, c := range cols {
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="12" text-anchor="middle">%s</text>`, labelW+float64(j)*(cellW+gap)+cellW/2, esc(c))
	}
	for i, r := range rows {
		y := 20 + float64(i)*(cellH+gap)
		fmt.Fprintf(&b, `<text class="label" x="%.1f" y="%.1f" text-anchor="end" dominant-baseline="middle">%s</text>`, labelW-8, y+cellH/2, esc(r))
		for j := range cols {
			v := values[i][j]
			if math.IsNaN(v) {
				continue
			}
			x := labelW + float64(j)*(cellW+gap)
			step := 1
			if maxV > minV {
				step = 1 + int(math.Round((v-minV)/(maxV-minV)*6))
			}
			step = min(max(step, 1), 7)
			fmt.Fprintf(&b, `<g class="mark" tabindex="0" data-tip-label="%s" data-tip-value="%s">`, esc(tip(i, j, v)), esc(cellText(v)))
			fmt.Fprintf(&b, `<rect class="seq%d" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="3"/>`, step, x, y, cellW, cellH)
			fmt.Fprintf(&b, `<text class="cell seq-text%d" x="%.1f" y="%.1f" text-anchor="middle" dominant-baseline="middle">%s</text>`, step, x+cellW/2, y+cellH/2, esc(cellText(v)))
			b.WriteString(`</g>`)
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// priceHistoryChart: a step line of a product's price versions.
func priceHistoryChart(prices []pricePoint, c *Customer) string {
	if len(prices) == 0 {
		return ""
	}
	const width, height = 520, 170
	now := time.Now()
	start := prices[0].ValidFrom
	span := now.Sub(start).Seconds()
	if span <= 0 {
		span = 1
	}
	maxV := 0.0
	for _, p := range prices {
		maxV = math.Max(maxV, p.PriceUSD)
	}
	ticks := niceTicks(maxV*1.1, 3)
	top := ticks[len(ticks)-1]
	plotW, plotH, y0 := float64(width-leftAxis-16), float64(height-axisBand-14), 14.0
	xOf := func(t time.Time) float64 { return float64(leftAxis) + t.Sub(start).Seconds()/span*plotW }
	yOf := func(v float64) float64 { return y0 + plotH - v/top*plotH }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %d %d" style="max-width: %dpx" role="img" aria-label="price history">`, width, height, width)
	for _, t := range ticks {
		fmt.Fprintf(&b, `<line class="grid" x1="%d" x2="%d" y1="%.1f" y2="%.1f"/>`, leftAxis, width-16, yOf(t), yOf(t))
		fmt.Fprintf(&b, `<text class="tick" x="%d" y="%.1f" text-anchor="end" dominant-baseline="middle">%s</text>`, leftAxis-6, yOf(t), esc(formatMoney(c, t)))
	}
	var path strings.Builder
	for i, p := range prices {
		end := now
		if p.ValidTo != nil {
			end = *p.ValidTo
		}
		if i == 0 {
			fmt.Fprintf(&path, "M%.1f,%.1f", xOf(p.ValidFrom), yOf(p.PriceUSD))
		} else {
			fmt.Fprintf(&path, " V%.1f", yOf(p.PriceUSD))
		}
		fmt.Fprintf(&path, " H%.1f", xOf(end))
	}
	fmt.Fprintf(&b, `<path class="line s1-stroke" d="%s"/>`, path.String())
	for _, p := range prices {
		end := "now"
		if p.ValidTo != nil {
			end = p.ValidTo.Format("Jan 2, 2006")
		}
		fmt.Fprintf(&b, `<g class="mark" tabindex="0" data-tip-label="%s" data-tip-value="%s">`,
			esc(p.ValidFrom.Format("Jan 2, 2006")+" → "+end), esc(formatMoney(c, p.PriceUSD)))
		fmt.Fprintf(&b, `<circle class="hit" cx="%.1f" cy="%.1f" r="12"/>`, xOf(p.ValidFrom), yOf(p.PriceUSD))
		fmt.Fprintf(&b, `<circle class="dot s1" cx="%.1f" cy="%.1f" r="4"/>`, xOf(p.ValidFrom), yOf(p.PriceUSD))
		b.WriteString(`</g>`)
	}
	last := prices[len(prices)-1]
	fmt.Fprintf(&b, `<text class="value" x="%d" y="%.1f" text-anchor="end">%s</text>`, width-16, yOf(last.PriceUSD)-8, esc(formatMoney(c, last.PriceUSD)))
	fmt.Fprintf(&b, `<line class="baseline" x1="%d" x2="%d" y1="%.1f" y2="%.1f"/>`, leftAxis, width-16, y0+plotH, y0+plotH)
	fmt.Fprintf(&b, `<text class="tick" x="%d" y="%.1f">%s</text>`, leftAxis, y0+plotH+16, esc(start.Format("Jan 2006")))
	fmt.Fprintf(&b, `<text class="tick" x="%d" y="%.1f" text-anchor="end">today</text>`, width-16, y0+plotH+16)
	b.WriteString(`</svg>`)
	return b.String()
}
