// Package seed fills the store's Postgres database with realistic fake data.
package seed

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/irlm/duckstore/internal/pg"
)

type Options struct {
	Scale float64   // 1 = ~300k customers, 30k products, 2.5M orders
	Seed  uint64    // same seed = same data
	End   time.Time // the dataset's "now"; orders cover [End-Years, End)
	Years int
	Log   io.Writer
}

func Run(ctx context.Context, pool *pgxpool.Pool, opt Options) error {
	logf := func(format string, args ...any) { fmt.Fprintf(opt.Log, format+"\n", args...) }
	began := time.Now()
	step := func(name string, fn func() error) error {
		t := time.Now()
		logf("▶ %s", name)
		if err := fn(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		logf("  done in %s", time.Since(t).Round(time.Millisecond))
		return nil
	}

	g := newGen(opt)
	logf("duckstore seed: scale=%g seed=%d, orders from %s to %s", opt.Scale, opt.Seed, g.start.Format(time.DateOnly), g.end.Format(time.DateOnly))

	if err := step("create schema (01_tables.sql)", func() error { return pg.ApplySchemaFile(ctx, pool, "01_tables.sql") }); err != nil {
		return err
	}
	if err := step("generate reference data in memory", func() error { g.buildReference(); return nil }); err != nil {
		return err
	}
	if err := step("COPY reference tables", func() error { return g.copyReference(ctx, pool, logf) }); err != nil {
		return err
	}
	if err := step("generate orders and COPY 6 tables in parallel", func() error { return g.copyOrders(ctx, pool, logf) }); err != nil {
		return err
	}
	if err := step("add primary keys, foreign keys and indexes (02_constraints.sql)", func() error {
		return pg.ApplySchemaFile(ctx, pool, "02_constraints.sql")
	}); err != nil {
		return err
	}
	// VACUUM cannot run inside a multi-statement batch, so it is its own call.
	if err := step("VACUUM ANALYZE (update planner statistics)", func() error {
		_, err := pool.Exec(ctx, "VACUUM (ANALYZE)")
		return err
	}); err != nil {
		return err
	}
	logf("✔ seed finished in %s", time.Since(began).Round(time.Second))
	return printCounts(ctx, pool, logf)
}

func (g *gen) copyReference(ctx context.Context, pool *pgxpool.Pool, logf func(string, ...any)) error {
	type job struct {
		table string
		run   func() (int64, error)
	}
	jobs := []job{
		{"currencies", func() (int64, error) {
			return copyN(ctx, pool, "currencies", []string{"code", "name", "symbol"}, len(currencyDefs), func(i int) []any {
				c := currencyDefs[i]
				return []any{c.Code, c.Name, c.Symbol}
			})
		}},
		{"countries", func() (int64, error) {
			return copyN(ctx, pool, "countries", []string{"code", "name", "region", "currency_code", "tax_rate"}, len(countryDefs), func(i int) []any {
				c := countryDefs[i]
				return []any{c.Code, c.Name, c.Region, c.Currency, decimal(c.TaxRate, 4)}
			})
		}},
		{"fx_rates", func() (int64, error) {
			return copyN(ctx, pool, "fx_rates", []string{"currency_code", "rate_date", "units_per_usd"}, len(g.fxRows), func(i int) []any {
				r := g.fxRows[i]
				return []any{currencyDefs[r.curr].Code, r.date, decimal(r.rate, 6)}
			})
		}},
		{"employees", func() (int64, error) {
			return copyN(ctx, pool, "employees", []string{"id", "manager_id", "first_name", "last_name", "email", "title", "department", "region", "warehouse_id", "hired_at", "salary_usd"}, len(g.employees), func(i int) []any {
				e := g.employees[i]
				var region any
				if e.region != "" {
					region = e.region
				}
				email := fmt.Sprintf("%s.%s.%d@duckstore.example", slugify(e.first), slugify(e.last), e.id)
				return []any{e.id, nullID(e.managerID), e.first, e.last, email, e.title, e.dept, region, nullID(e.warehouseID), e.hiredAt, money(e.salaryCents)}
			})
		}},
		{"warehouses", func() (int64, error) {
			return copyN(ctx, pool, "warehouses", []string{"id", "code", "name", "country_code", "city", "manager_employee_id"}, len(warehouseDefs), func(i int) []any {
				w := warehouseDefs[i]
				return []any{int64(i + 1), w.Code, w.Name, w.Country, w.City, nullID(g.whManagers[i])}
			})
		}},
		{"categories", func() (int64, error) {
			return copyN(ctx, pool, "categories", []string{"id", "parent_id", "name", "slug"}, len(g.categories), func(i int) []any {
				c := g.categories[i]
				return []any{c.id, nullID(c.parentID), c.name, c.slug}
			})
		}},
		{"brands", func() (int64, error) {
			return copyN(ctx, pool, "brands", []string{"id", "name"}, len(g.brandNames), func(i int) []any {
				return []any{int64(i + 1), g.brandNames[i]}
			})
		}},
		{"suppliers", func() (int64, error) {
			return copyN(ctx, pool, "suppliers", []string{"id", "name", "country_code", "rating"}, len(g.suppliers), func(i int) []any {
				s := g.suppliers[i]
				return []any{int64(i + 1), s.name, s.country, s.rating}
			})
		}},
		{"products", func() (int64, error) {
			return copyN(ctx, pool, "products", []string{"id", "sku", "name", "description", "category_id", "brand_id", "price_usd", "cost_usd", "weight_kg", "attributes", "is_active", "created_at"}, len(g.products), func(i int) []any {
				p := &g.products[i]
				current := p.prices[len(p.prices)-1].cents
				return []any{int64(i + 1), p.sku, p.name, p.desc, g.categories[p.cat].id, int64(p.brand), money(current), money(p.costCents), decimal(p.weightKg, 3), p.attrs, g.rng.Float64() > 0.02, p.createdAt}
			})
		}},
		{"product_prices", func() (int64, error) {
			pi, vi := 0, 0
			return copyIter(ctx, pool, "product_prices", []string{"product_id", "valid_from", "valid_to", "price_usd"}, func() []any {
				for pi < len(g.products) && vi >= len(g.products[pi].prices) {
					pi, vi = pi+1, 0
				}
				if pi >= len(g.products) {
					return nil
				}
				prices := g.products[pi].prices
				var validTo any
				if vi+1 < len(prices) {
					validTo = prices[vi+1].from
				}
				row := []any{int64(pi + 1), prices[vi].from, validTo, money(prices[vi].cents)}
				vi++
				return row
			})
		}},
		{"product_suppliers", func() (int64, error) {
			var rows [][]any
			for i := range g.products {
				n := 1 + g.weighted([]float64{50, 35, 15})
				seen := map[int]bool{}
				for k := range n {
					s := g.rng.IntN(len(g.suppliers))
					if seen[s] {
						continue
					}
					seen[s] = true
					cost := int64(float64(g.products[i].costCents) * g.uniform(0.9, 1.05))
					rows = append(rows, []any{int64(i + 1), int64(s + 1), money(cost), int32(3 + g.rng.IntN(43)), k == 0})
				}
			}
			return copyN(ctx, pool, "product_suppliers", []string{"product_id", "supplier_id", "unit_cost_usd", "lead_time_days", "is_primary"}, len(rows), func(i int) []any { return rows[i] })
		}},
		{"inventory", func() (int64, error) {
			rank := make([]int, len(g.products))
			for r, pi := range g.popularity {
				rank[pi] = r
			}
			var rows [][]any
			for i := range g.products {
				n := 2 + g.rng.IntN(3)
				seen := map[int]bool{}
				for range n {
					w := g.rng.IntN(len(warehouseDefs))
					if seen[w] {
						continue
					}
					seen[w] = true
					qty := g.rng.IntN(200)
					if rank[i] < 1000 {
						qty = 50 + g.rng.IntN(750)
					}
					if g.rng.Float64() < 0.05 {
						qty = 0
					}
					rows = append(rows, []any{int64(w + 1), int64(i + 1), int32(qty), int32(5 + g.rng.IntN(46)), g.end})
				}
			}
			return copyN(ctx, pool, "inventory", []string{"warehouse_id", "product_id", "quantity_on_hand", "reorder_point", "updated_at"}, len(rows), func(i int) []any { return rows[i] })
		}},
		{"promotions", func() (int64, error) {
			return copyN(ctx, pool, "promotions", []string{"id", "code", "name", "discount_pct", "starts_at", "ends_at"}, len(g.promotions), func(i int) []any {
				p := g.promotions[i]
				return []any{int64(i + 1), p.code, p.name, decimal(p.pct, 2), p.starts, p.ends}
			})
		}},
		{"promotion_products", func() (int64, error) {
			pi, k := 0, 0
			return copyIter(ctx, pool, "promotion_products", []string{"promotion_id", "product_id"}, func() []any {
				for pi < len(g.promotions) && k >= len(g.promotions[pi].products) {
					pi, k = pi+1, 0
				}
				if pi >= len(g.promotions) {
					return nil
				}
				row := []any{int64(pi + 1), int64(g.promotions[pi].products[k] + 1)}
				k++
				return row
			})
		}},
		{"customers", func() (int64, error) {
			return copyN(ctx, pool, "customers", []string{"id", "email", "first_name", "last_name", "segment", "country_code", "referred_by_customer_id", "account_manager_id", "created_at"}, len(g.customers), func(i int) []any {
				c := &g.customers[i]
				first, last := firstNames[c.first], lastNames[c.last]
				email := fmt.Sprintf("%s.%s.%d@example.com", slugify(first), slugify(last), i+1)
				return []any{int64(i + 1), email, first, last, segmentNames[c.segment], countryDefs[c.country].Code, nullID(c.referredBy), nullID(c.accountMgr), c.createdAt}
			})
		}},
		{"addresses", func() (int64, error) {
			ci, k := 0, 0
			return copyIter(ctx, pool, "addresses", []string{"id", "customer_id", "kind", "line1", "city", "postal_code", "country_code", "is_default"}, func() []any {
				for ci < len(g.customers) && k >= int(g.customers[ci].addrCount) {
					ci, k = ci+1, 0
				}
				if ci >= len(g.customers) {
					return nil
				}
				c := &g.customers[ci]
				cd := &countryDefs[c.country]
				id := c.addrID + int64(k)
				line := fmt.Sprintf("%d %s", c.streetNo, streetNames[c.street])
				postal := fmt.Sprintf("%05d", (int(c.streetNo)*37+ci)%100000)
				var row []any
				switch k {
				case 0:
					row = []any{id, int64(ci + 1), "shipping", line, cd.Cities[c.city], postal, cd.Code, true}
				case 1:
					row = []any{id, int64(ci + 1), "billing", line, cd.Cities[c.city], postal, cd.Code, true}
				default:
					row = []any{id, int64(ci + 1), "shipping", fmt.Sprintf("%d %s", (int(c.streetNo)*7)%900+1, streetNames[(int(c.street)+5)%len(streetNames)]), cd.Cities[c.extraCity], postal, cd.Code, false}
				}
				k++
				return row
			})
		}},
	}
	for _, j := range jobs {
		t := time.Now()
		n, err := j.run()
		if err != nil {
			return fmt.Errorf("copy %s: %w", j.table, err)
		}
		logf("  %-20s %10s rows  %s", j.table, fmtInt(n), time.Since(t).Round(time.Millisecond))
	}
	return nil
}

func (g *gen) copyOrders(ctx context.Context, pool *pgxpool.Pool, logf func(string, ...any)) error {
	s := newOrderStreams()
	grp, gctx := errgroup.WithContext(ctx)
	for _, st := range s.all() {
		grp.Go(func() error { return st.copy(gctx, pool) })
	}
	grp.Go(func() error { return g.generateOrders(gctx, s) })

	done := make(chan struct{})
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				logf("  … %s orders, %s order_items so far", fmtInt(s.orders.rows.Load()), fmtInt(s.items.rows.Load()))
			}
		}
	}()
	err := grp.Wait()
	close(done)
	if err != nil {
		return err
	}
	for _, st := range s.all() {
		logf("  %-20s %10s rows", st.table, fmtInt(st.rows.Load()))
	}
	return nil
}

func printCounts(ctx context.Context, pool *pgxpool.Pool, logf func(string, ...any)) error {
	rows, err := pool.Query(ctx, `
		SELECT c.relname, pg_size_pretty(pg_total_relation_size(c.oid)), c.reltuples::bigint
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'store' AND c.relkind = 'r'
		ORDER BY pg_total_relation_size(c.oid) DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	logf("\n%-20s %12s %14s", "table", "size", "rows (approx)")
	for rows.Next() {
		var name, size string
		var n int64
		if err := rows.Scan(&name, &size, &n); err != nil {
			return err
		}
		logf("%-20s %12s %14s", name, size, fmtInt(max(n, 0)))
	}
	return rows.Err()
}

func fmtInt(n int64) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
