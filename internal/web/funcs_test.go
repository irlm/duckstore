package web

import (
	"strings"
	"testing"
	"time"
)

func TestCommas(t *testing.T) {
	tests := []struct {
		v    float64
		d    int
		want string
	}{
		{0, 0, "0"}, {999, 0, "999"}, {1000, 0, "1,000"}, {1234567.891, 2, "1,234,567.89"}, {-45678.5, 1, "-45,678.5"},
	}
	for _, tt := range tests {
		if got := commas(tt.v, tt.d); got != tt.want {
			t.Errorf("commas(%v, %d) = %q, want %q", tt.v, tt.d, got, tt.want)
		}
	}
}

func TestNiceTicks(t *testing.T) {
	ticks := niceTicks(17_300_000, 4)
	if ticks[0] != 0 || ticks[len(ticks)-1] < 17_300_000 {
		t.Fatalf("ticks %v must start at 0 and cover the max", ticks)
	}
	step := ticks[1] - ticks[0]
	if step != 5_000_000 {
		t.Errorf("step = %v, want a round 5,000,000", step)
	}
}

func TestFormatHelpers(t *testing.T) {
	if got := formatDuration(1500 * time.Microsecond); got != "1.5 ms" {
		t.Errorf("formatDuration = %q", got)
	}
	if got := formatDuration(2346 * time.Millisecond); got != "2.35 s" {
		t.Errorf("formatDuration = %q", got)
	}
	if got := compactUSD(4_200_000); got != "$4.2M" {
		t.Errorf("compactUSD = %q", got)
	}
	jpy := &Customer{Currency: "JPY", Symbol: "¥", FX: 150}
	if got := formatMoney(jpy, 10); got != "¥1,500" {
		t.Errorf("formatMoney JPY = %q", got)
	}
	if got := centsText(-1205); got != "-12.05" {
		t.Errorf("centsText = %q", got)
	}
}

func TestChartsEscapeLabels(t *testing.T) {
	svg := hbarChart([]hbar{{Label: `<script>"x"</script>`, Value: 1, ValueText: "1", Slot: 1}}, 400, 150)
	if strings.Contains(svg, "<script>") {
		t.Fatal("chart labels must be HTML-escaped")
	}
}
