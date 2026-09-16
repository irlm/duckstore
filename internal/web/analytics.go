package web

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/irlm/duckstore/internal/query"
)

// ---------------------------------------------------------------------------
// The analytics dashboard runs on the DuckDB warehouse. Each widget is one
// question over millions of rows. They run in parallel: DuckDB executes many
// queries at once inside one process, each using several CPU cores.
// ---------------------------------------------------------------------------

type widget struct {
	ID       string
	Title    string
	Question string
	Lesson   string // what the SQL demonstrates
	SQL      string
	Result   *query.Result
	Err      string
	Chart    template.HTML
}

const sqlETLInfo = `-- when was the warehouse built, and up to which order?
SELECT built_at, max_order_id, build_seconds, data_until
FROM dw.etl_info`

const sqlKPIs = `-- last 30 days vs the 30 days before
WITH bounds AS (
    SELECT max(order_date) AS last_day FROM dw.fact_orders
),
periods AS (
    SELECT CASE WHEN fo.order_date > b.last_day - 30 THEN 'current' ELSE 'previous' END AS period,
           fo.*
    FROM dw.fact_orders fo, bounds b
    WHERE fo.order_date > b.last_day - 60
      AND fo.status <> 'cancelled'
)
SELECT period,
       sum(total_usd)                                        AS revenue_usd,
       count(*)                                              AS orders,
       sum(total_usd) / count(*)                             AS avg_order_usd,
       count(*) FILTER (WHERE customer_order_number = 1)     AS new_customers,
       count(DISTINCT customer_id)                           AS active_customers
FROM periods
GROUP BY period`

var dashboardWidgets = []widget{
	{
		ID:       "monthly",
		Title:    "Revenue per month (USD)",
		Question: "How is the business growing? Every order line converted to USD, grouped by month.",
		Lesson:   "date_trunc + sum over 5M rows. Only the order_date and net_usd columns are read from disk.",
		SQL: `SELECT date_trunc('month', order_date) AS month,
       sum(net_usd)             AS revenue_usd,
       count(DISTINCT order_id) AS orders
FROM dw.fact_sales
GROUP BY ALL
ORDER BY month`,
	},
	{
		ID:       "pivot",
		Title:    "Revenue by category and year (USD millions)",
		Question: "Which categories grow? One column per year, created by PIVOT.",
		Lesson:   "PIVOT without listing the years: DuckDB finds the values itself. In T-SQL this needs dynamic SQL.",
		SQL: `PIVOT (
    SELECT dp.category_l1 AS category, year(fs.order_date) AS year, fs.net_usd
    FROM dw.fact_sales fs
    JOIN dw.dim_product dp USING (product_key)
)
ON year
USING round(sum(net_usd) / 1e6, 2)
GROUP BY category
ORDER BY category`,
	},
	{
		ID:       "top-products",
		Title:    "Top 10 products, last 90 days",
		Question: "What sells most right now?",
		Lesson:   "A scalar subquery for the date window, GROUP BY ALL, and the SCD2 dimension (product_key = the version sold).",
		SQL: `SELECT dp.product_name,
       dp.brand,
       sum(fs.quantity) AS units,
       sum(fs.net_usd)  AS revenue_usd
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
WHERE fs.order_date > (SELECT max(order_date) FROM dw.fact_sales) - INTERVAL 90 DAY
GROUP BY ALL
ORDER BY revenue_usd DESC
LIMIT 10`,
	},
	{
		ID:       "cohorts",
		Title:    "Customer retention by first-purchase month",
		Question: "Of the customers whose first order was in month X, what share ordered again N months later?",
		Lesson:   "Two CTEs, date_diff, and first_value() as a window function to divide by the cohort size.",
		SQL: `WITH firsts AS (
    SELECT customer_id, date_trunc('month', min(order_date)) AS cohort
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
    GROUP BY ALL
),
activity AS (
    SELECT DISTINCT customer_id, date_trunc('month', order_date) AS month
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
),
counts AS (
    SELECT f.cohort, date_diff('month', f.cohort, a.month) AS month_n, count(*) AS customers
    FROM firsts f
    JOIN activity a USING (customer_id)
    GROUP BY ALL
)
SELECT cohort, month_n, customers,
       round(100.0 * customers / first_value(customers) OVER (PARTITION BY cohort ORDER BY month_n), 1) AS pct
FROM counts
WHERE cohort >= (SELECT date_trunc('month', max(order_date)) - INTERVAL 12 MONTH FROM dw.fact_orders)
  AND month_n <= 11
ORDER BY cohort, month_n`,
	},
	{
		ID:       "returns",
		Title:    "Highest return rates by category",
		Question: "Where do returns hurt, and why do customers send things back?",
		Lesson:   "LEFT JOIN on a composite key with USING (order_id, line_no), and mode() for the most common reason.",
		SQL: `SELECT dp.category_leaf AS category,
       dp.category_l1         AS department,
       sum(fs.quantity)                                                   AS units_sold,
       coalesce(sum(fr.quantity), 0)                                      AS units_returned,
       round(100.0 * coalesce(sum(fr.quantity), 0) / sum(fs.quantity), 2) AS return_rate_pct,
       mode(fr.reason)                                                    AS top_reason
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
LEFT JOIN dw.fact_returns fr USING (order_id, line_no)
WHERE fs.order_status = 'delivered'
GROUP BY ALL
ORDER BY return_rate_pct DESC
LIMIT 10`,
	},
	{
		ID:       "carriers",
		Title:    "Delivery time by carrier (days)",
		Question: "Which carriers are slow, and how bad is the slow tail?",
		Lesson:   "quantile_cont for percentiles (PERCENTILE_CONT ... WITHIN GROUP in T-SQL) and FILTER on an aggregate.",
		SQL: `SELECT carrier,
       count(*)                                                    AS deliveries,
       round(quantile_cont(transit_days, 0.5), 1)                  AS p50_days,
       round(quantile_cont(transit_days, 0.9), 1)                  AS p90_days,
       round(100.0 * count(*) FILTER (WHERE transit_days > 7) / count(*), 1) AS over_7_days_pct
FROM dw.fact_orders
WHERE delivered_at IS NOT NULL
GROUP BY ALL
ORDER BY p90_days DESC`,
	},
	{
		ID:       "sales-teams",
		Title:    "Revenue from business and VIP customers, by sales leader (last 12 months)",
		Question: "How much revenue does each director and manager own, counting everyone below them?",
		Lesson:   "A hierarchy rollup without recursion at query time: list_contains() on the management chain list built by the ETL.",
		SQL: `WITH customer_revenue AS (
    SELECT dc.account_manager_id, sum(fs.net_usd) AS revenue
    FROM dw.fact_sales fs
    JOIN dw.dim_customer dc USING (customer_id)
    WHERE dc.account_manager_id IS NOT NULL
      AND fs.order_date > (SELECT max(order_date) FROM dw.fact_sales) - INTERVAL 365 DAY
    GROUP BY ALL
)
SELECT leader.level,
       leader.name,
       leader.title,
       count(DISTINCT ae.employee_id) AS account_executives,
       sum(cr.revenue)                AS revenue_usd
FROM dw.dim_employee leader
JOIN dw.dim_employee ae ON list_contains(ae.management_chain_ids, leader.employee_id)
JOIN customer_revenue cr ON cr.account_manager_id = ae.employee_id
WHERE leader.department = 'Sales' AND leader.level <= 3
GROUP BY ALL
ORDER BY leader.level, revenue_usd DESC`,
	},
	{
		ID:       "anomalies",
		Title:    "Unusual days",
		Question: "Which days had far fewer or far more orders than the week before? (outages, Black Friday)",
		Lesson:   "A moving average with a window frame: ROWS BETWEEN 7 PRECEDING AND 1 PRECEDING.",
		SQL: `WITH daily AS (
    SELECT order_date, count(*) AS orders
    FROM dw.fact_orders
    GROUP BY ALL
),
scored AS (
    SELECT order_date, orders,
           avg(orders) OVER (ORDER BY order_date ROWS BETWEEN 7 PRECEDING AND 1 PRECEDING) AS avg_prev_7d
    FROM daily
)
SELECT order_date, orders, round(avg_prev_7d) AS avg_prev_7d,
       round(100.0 * orders / avg_prev_7d - 100, 1) AS change_pct
FROM scored
WHERE order_date > (SELECT min(order_date) + 30 FROM daily)
  AND (orders < 0.6 * avg_prev_7d OR orders > 1.8 * avg_prev_7d)
ORDER BY order_date DESC
LIMIT 12`,
	},
}

type kpi struct {
	Label, Value, Delta string
	Up, Good            bool
	HasDelta            bool
}

type dashboardData struct {
	Info     map[string]any
	InfoErr  string
	Live     map[string]any
	LiveErr  string
	LiveSQL  string
	KPIs     []kpi
	Widgets  []*widget
	Elapsed  time.Duration
	PGAttach string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, p *pageData) (*view, error) {
	ctx := r.Context()
	d := &dashboardData{}
	start := time.Now()

	info, err := duck(ctx, s.wh, sqlETLInfo, 1)
	if err != nil {
		return nil, err
	}
	d.Info = rowMap(info)
	if pgErr := s.wh.PostgresAttachError(); pgErr != nil {
		d.PGAttach = pgErr.Error()
	}

	var wg sync.WaitGroup
	wg.Go(func() { d.KPIs = s.kpis(ctx) })
	wg.Go(func() { d.Live, d.LiveSQL, d.LiveErr = s.liveSinceETL(ctx, d.Info) })
	for i := range dashboardWidgets {
		wd := dashboardWidgets[i] // copy
		d.Widgets = append(d.Widgets, &wd)
	}
	for _, wd := range d.Widgets {
		wg.Go(func() {
			res, err := duck(ctx, s.wh, wd.SQL, 200)
			if err != nil {
				wd.Err = err.Error()
				return
			}
			wd.Result = res
			wd.Chart = template.HTML(widgetChart(wd.ID, res))
		})
	}
	wg.Wait()
	d.Elapsed = time.Since(start)
	return &view{Template: "analytics", Title: "Analytics", Data: d}, nil
}

func (s *Server) kpis(ctx context.Context) []kpi {
	res, err := duck(ctx, s.wh, sqlKPIs, 0)
	if err != nil {
		return nil
	}
	byPeriod := map[string]map[string]float64{}
	for _, row := range res.Rows {
		m := map[string]float64{}
		for i, c := range res.Columns[1:] {
			m[c], _ = query.Float(row[i+1])
		}
		byPeriod[query.Format(row[0])] = m
	}
	cur, prev := byPeriod["current"], byPeriod["previous"]
	if cur == nil {
		return nil
	}
	mk := func(label, key string, format func(float64) string, upIsGood bool) kpi {
		k := kpi{Label: label, Value: format(cur[key])}
		if prev != nil && prev[key] != 0 {
			change := (cur[key] - prev[key]) / prev[key] * 100
			k.HasDelta = true
			k.Up = change >= 0
			k.Good = k.Up == upIsGood
			k.Delta = fmt.Sprintf("%+.1f%%", change)
		}
		return k
	}
	return []kpi{
		mk("Revenue", "revenue_usd", compactUSD, true),
		mk("Orders", "orders", func(v float64) string { return commas(v, 0) }, true),
		mk("Average order", "avg_order_usd", func(v float64) string { return "$" + commas(v, 2) }, true),
		mk("New customers", "new_customers", func(v float64) string { return commas(v, 0) }, true),
		mk("Active customers", "active_customers", func(v float64) string { return commas(v, 0) }, true),
	}
}

// liveSinceETL is a HYBRID query: DuckDB reads Postgres directly (ATTACH ...
// TYPE postgres) for the orders the warehouse does not have yet. The
// watermark goes into the SQL as a literal so the filter is pushed down to
// Postgres and only the new rows travel.
func (s *Server) liveSinceETL(ctx context.Context, info map[string]any) (map[string]any, string, string) {
	maxID := toInt(info["max_order_id"])
	sql := fmt.Sprintf(`-- orders placed since the last ETL, read LIVE from Postgres by DuckDB
SELECT count(*)                               AS orders,
       count(DISTINCT customer_id)            AS customers,
       coalesce(max(placed_at)::TIMESTAMP, NULL) AS last_order_at
FROM pg.store.orders
WHERE id > %d`, maxID)
	res, err := duck(ctx, s.wh, sql, 1)
	if err != nil {
		return nil, sql, err.Error()
	}
	return rowMap(res), sql, ""
}

func rowMap(res *query.Result) map[string]any {
	m := map[string]any{}
	if res == nil || len(res.Rows) == 0 {
		return m
	}
	for i, c := range res.Columns {
		m[c] = res.Rows[0][i]
	}
	return m
}

func widgetChart(id string, res *query.Result) string {
	switch id {
	case "monthly":
		cols := make([]column, len(res.Rows))
		for i, row := range res.Rows {
			t, _ := row[0].(time.Time)
			v, _ := query.Float(row[1])
			label := ""
			if t.Month() == time.January || i == 0 {
				label = t.Format("2006")
			}
			tip := t.Format("January 2006")
			if i == 0 || i == len(res.Rows)-1 {
				tip += " (partial month)"
			}
			cols[i] = column{Label: label, TipLabel: tip, Value: v, ValueText: compactUSD(v)}
		}
		return columnChart(cols, 1060, 280, compactUSD, false)
	case "top-products":
		bars := make([]hbar, len(res.Rows))
		for i, row := range res.Rows {
			v, _ := query.Float(row[3])
			bars[i] = hbar{Label: query.Format(row[0]), Value: v, ValueText: compactUSD(v), Slot: 1}
		}
		return hbarChart(bars, 760, 280)
	case "returns":
		bars := make([]hbar, len(res.Rows))
		for i, row := range res.Rows {
			v, _ := query.Float(row[4])
			bars[i] = hbar{Label: query.Format(row[0]), Value: v, ValueText: fmt.Sprintf("%.1f%% · %s", v, query.Format(row[5])), Slot: 1}
		}
		return hbarChart(bars, 760, 170)
	case "cohorts":
		return cohortHeatmap(res)
	}
	return ""
}

func cohortHeatmap(res *query.Result) string {
	var rows []string
	rowIdx := map[string]int{}
	cols := make([]string, 12)
	for i := range cols {
		cols[i] = fmt.Sprintf("M%d", i)
	}
	var cells [][]float64
	for _, row := range res.Rows {
		t, _ := row[0].(time.Time)
		label := t.Format("Jan 2006")
		i, ok := rowIdx[label]
		if !ok {
			i = len(rows)
			rowIdx[label] = i
			rows = append(rows, label)
			line := make([]float64, 12)
			for j := range line {
				line[j] = math.NaN()
			}
			cells = append(cells, line)
		}
		n, _ := query.Float(row[1])
		pct, _ := query.Float(row[3])
		if int(n) < 12 {
			cells[i][int(n)] = pct
		}
	}
	// The scale ignores month 0 (always 100%) so the later months use the whole ramp.
	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, line := range cells {
		for j, v := range line[1:] {
			_ = j
			if !math.IsNaN(v) {
				minV, maxV = math.Min(minV, v), math.Max(maxV, v)
			}
		}
	}
	if math.IsInf(minV, 1) {
		minV, maxV = 0, 100
	}
	return heatmap(rows, cols, cells,
		func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
		func(r, c int, v float64) string { return fmt.Sprintf("%s cohort, %d months later", rows[r], c) },
		minV, maxV)
}
