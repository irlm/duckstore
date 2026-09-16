package seed

import (
	"testing"
	"time"
)

func testGen(seed uint64) *gen {
	g := newGen(Options{Scale: 0.01, Seed: seed, End: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Years: 3})
	g.buildReference()
	return g
}

func TestSameSeedSameData(t *testing.T) {
	a, b := testGen(7), testGen(7)
	if len(a.products) != len(b.products) || len(a.customers) != len(b.customers) {
		t.Fatalf("sizes differ: %d/%d products, %d/%d customers", len(a.products), len(b.products), len(a.customers), len(b.customers))
	}
	for i := range a.products {
		if a.products[i].name != b.products[i].name || a.products[i].prices[0] != b.products[i].prices[0] {
			t.Fatalf("product %d differs: %q vs %q", i, a.products[i].name, b.products[i].name)
		}
	}
	if c := testGen(8); c.products[0].name == a.products[0].name && c.products[1].name == a.products[1].name {
		t.Error("a different seed should give different products")
	}
}

func TestReferenceDataIsConsistent(t *testing.T) {
	g := testGen(42)
	for i, c := range g.customers {
		if i > 0 && c.createdAt.Before(g.customers[0].createdAt) {
			t.Fatalf("customer %d created before the first customer", i+1)
		}
		if c.referredBy != 0 && int(c.referredBy) >= i+1 {
			t.Fatalf("customer %d referred by a later customer %d", i+1, c.referredBy)
		}
	}
	for i, p := range g.products {
		for j := 1; j < len(p.prices); j++ {
			if !p.prices[j].from.After(p.prices[j-1].from) {
				t.Fatalf("product %d price versions out of order", i+1)
			}
		}
	}
	for _, e := range g.employees {
		if e.managerID >= e.id {
			t.Fatalf("employee %d has a manager created after them (%d)", e.id, e.managerID)
		}
	}
}

func TestPriceAt(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2025, 1, d, 0, 0, 0, 0, time.UTC) }
	p := product{prices: []pricePoint{{day(1), 1000}, {day(10), 1200}, {day(20), 900}}}
	tests := []struct {
		at   time.Time
		want int64
	}{
		{day(1), 1000}, {day(5), 1000}, {day(10), 1200}, {day(19), 1200}, {day(20), 900}, {day(31), 900},
	}
	for _, tt := range tests {
		if got := p.priceAt(tt.at); got != tt.want {
			t.Errorf("priceAt(%s) = %d, want %d", tt.at.Format(time.DateOnly), got, tt.want)
		}
	}
}

func TestRateAtUsesLastBusinessDay(t *testing.T) {
	g := testGen(42)
	eur := g.currIdx["EUR"]
	// Find a Saturday and check it uses Friday's rate.
	for d := g.start; d.Before(g.end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday {
			continue
		}
		friday := g.rateAt(eur, d.AddDate(0, 0, -1))
		if got := g.rateAt(eur, d.Add(15*time.Hour)); got != friday {
			t.Fatalf("Saturday %s rate %v, want Friday rate %v", d.Format(time.DateOnly), got, friday)
		}
		return
	}
	t.Fatal("no Saturday found")
}

func TestBlackFriday(t *testing.T) {
	for year, want := range map[int]string{2024: "2024-11-29", 2025: "2025-11-28", 2026: "2026-11-27"} {
		if got := blackFriday(year).Format(time.DateOnly); got != want {
			t.Errorf("blackFriday(%d) = %s, want %s", year, got, want)
		}
	}
}

func TestCharmPriceAndSlugify(t *testing.T) {
	if got := charmPrice(23.4); got != 2299 {
		t.Errorf("charmPrice(23.4) = %d, want 2299", got)
	}
	if got := slugify("Home & Kitchen"); got != "home-and-kitchen" {
		t.Errorf("slugify = %q", got)
	}
	if got := slugify("Müller"); got != "muller" {
		t.Errorf("slugify = %q", got)
	}
}
