package bench

// The questions of the engine race. Each read scenario has up to three
// variants of the same question:
//
//	postgres     Postgres, normalized store tables (what the app writes to)
//	duckdb_raw   DuckDB, the SAME normalized tables copied by the ETL (raw.*)
//	duckdb_star  DuckDB, the star schema built by the ETL (dw.*)
//
// postgres vs duckdb_raw  = the engine difference (row store vs column store)
// duckdb_raw vs duckdb_star = the data model difference (joins done once, in the ETL)

const (
	KeyPostgres   = "postgres"
	KeyDuckDBRaw  = "duckdb_raw"
	KeyDuckDBStar = "duckdb_star"
)

var scenarios = []*Scenario{
	{
		ID:       "monthly-revenue",
		Kind:     KindAnalytics,
		Title:    "Revenue per month, in USD, for all history",
		Question: "Scan every order line, convert each to USD with the rate of its order date, group by month.",
		Why: "Postgres reads whole rows (all columns) of 5M order lines and looks up an exchange rate per order with LATERAL. " +
			"DuckDB reads only the few columns it needs, compressed, on all CPU cores, and matches exchange rates with a single ASOF JOIN. " +
			"The star schema did the conversion once during the ETL, so the last variant only sums one column.",
		Variants: []Variant{
			{Key: KeyPostgres, SQL: `SELECT date_trunc('month', o.placed_at)::date AS month,
       round(sum((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd), 2) AS revenue_usd,
       count(DISTINCT o.id) AS orders
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
CROSS JOIN LATERAL (
    SELECT f.units_per_usd
    FROM fx_rates f
    WHERE f.currency_code = o.currency_code
      AND f.rate_date <= o.placed_at::date
    ORDER BY f.rate_date DESC
    LIMIT 1
) fx
WHERE o.status <> 'cancelled'
GROUP BY 1
ORDER BY 1`},
			{Key: KeyDuckDBRaw, SQL: `SELECT date_trunc('month', o.placed_at)::DATE AS month,
       round(sum((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd), 2) AS revenue_usd,
       count(DISTINCT o.id) AS orders
FROM raw.orders o
JOIN raw.order_items oi ON oi.order_id = o.id
ASOF JOIN raw.fx_rates fx
  ON fx.currency_code = o.currency_code
 AND o.placed_at::DATE >= fx.rate_date
WHERE o.status <> 'cancelled'
GROUP BY 1
ORDER BY 1`},
			{Key: KeyDuckDBStar, SQL: `SELECT date_trunc('month', order_date) AS month,
       sum(net_usd) AS revenue_usd,
       count(DISTINCT order_id) AS orders
FROM dw.fact_sales
GROUP BY 1
ORDER BY 1`},
		},
	},
	{
		ID:       "category-rollup",
		Kind:     KindAnalytics,
		Title:    "Revenue by category tree level with subtotals (ROLLUP)",
		Question: "Walk the category tree, join every sale to its top two category levels, and add subtotals and a grand total.",
		Why: "The normalized model needs a recursive CTE over the category tree plus the currency lookup on every query. " +
			"The star schema flattened the tree into columns (category_l1, category_l2) once, in the ETL.",
		Variants: []Variant{
			{Key: KeyPostgres, SQL: `WITH RECURSIVE tree AS (
    SELECT id, name AS level1, NULL::text AS level2, 1 AS depth
    FROM categories WHERE parent_id IS NULL
    UNION ALL
    SELECT c.id, t.level1, CASE WHEN t.depth = 1 THEN c.name ELSE t.level2 END, t.depth + 1
    FROM categories c JOIN tree t ON c.parent_id = t.id
)
SELECT t.level1, t.level2,
       count(DISTINCT o.id) AS orders,
       round(sum((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd), 2) AS revenue_usd
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
JOIN products p ON p.id = oi.product_id
JOIN tree t ON t.id = p.category_id
CROSS JOIN LATERAL (
    SELECT f.units_per_usd FROM fx_rates f
    WHERE f.currency_code = o.currency_code AND f.rate_date <= o.placed_at::date
    ORDER BY f.rate_date DESC LIMIT 1
) fx
WHERE o.status <> 'cancelled'
GROUP BY ROLLUP (t.level1, t.level2)
ORDER BY t.level1 NULLS LAST, t.level2 NULLS FIRST`},
			{Key: KeyDuckDBRaw, SQL: `WITH RECURSIVE tree AS (
    SELECT id, name AS level1, NULL::VARCHAR AS level2, 1 AS depth
    FROM raw.categories WHERE parent_id IS NULL
    UNION ALL
    SELECT c.id, t.level1, CASE WHEN t.depth = 1 THEN c.name ELSE t.level2 END, t.depth + 1
    FROM raw.categories c JOIN tree t ON c.parent_id = t.id
)
SELECT t.level1, t.level2,
       count(DISTINCT o.id) AS orders,
       round(sum((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd), 2) AS revenue_usd
FROM raw.orders o
JOIN raw.order_items oi ON oi.order_id = o.id
JOIN raw.products p ON p.id = oi.product_id
JOIN tree t ON t.id = p.category_id
ASOF JOIN raw.fx_rates fx
  ON fx.currency_code = o.currency_code AND o.placed_at::DATE >= fx.rate_date
WHERE o.status <> 'cancelled'
GROUP BY ROLLUP (t.level1, t.level2)
ORDER BY t.level1 NULLS LAST, t.level2 NULLS FIRST`},
			{Key: KeyDuckDBStar, SQL: `SELECT dp.category_l1 AS level1, dp.category_l2 AS level2,
       count(DISTINCT fs.order_id) AS orders,
       sum(fs.net_usd) AS revenue_usd
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
GROUP BY ROLLUP (dp.category_l1, dp.category_l2)
ORDER BY level1 NULLS LAST, level2 NULLS FIRST`},
		},
	},
	{
		ID:       "active-customers",
		Kind:     KindAnalytics,
		Title:    "Distinct active customers per month",
		Question: "COUNT(DISTINCT customer_id) per month over 2.5M orders.",
		Why: "COUNT(DISTINCT) must remember every value it has seen. Postgres does it by sorting inside each group; " +
			"DuckDB uses parallel hash tables on a single column. Both variants in DuckDB read the same one table, so they are close.",
		Variants: []Variant{
			{Key: KeyPostgres, SQL: `SELECT date_trunc('month', placed_at)::date AS month,
       count(DISTINCT customer_id) AS active_customers,
       count(*) AS orders
FROM orders
WHERE status <> 'cancelled'
GROUP BY 1
ORDER BY 1`},
			{Key: KeyDuckDBRaw, SQL: `SELECT date_trunc('month', placed_at)::DATE AS month,
       count(DISTINCT customer_id) AS active_customers,
       count(*) AS orders
FROM raw.orders
WHERE status <> 'cancelled'
GROUP BY 1
ORDER BY 1`},
			{Key: KeyDuckDBStar, SQL: `SELECT date_trunc('month', order_date) AS month,
       count(DISTINCT customer_id) AS active_customers,
       count(*) AS orders
FROM dw.fact_orders
WHERE status <> 'cancelled'
GROUP BY 1
ORDER BY 1`},
		},
	},
	{
		ID:       "cohort-retention",
		Kind:     KindAnalytics,
		Title:    "Cohort retention: of the customers who first bought in month X, how many bought again N months later?",
		Question: "Find each customer's first month, every month they were active, and count per (cohort, months later).",
		Why: "Two passes over all orders with DISTINCT and a join between them. Heavy for a row store, routine for a vectorized engine. " +
			"Note the dialect difference: Postgres has age(), DuckDB has date_diff().",
		Variants: []Variant{
			{Key: KeyPostgres, SQL: `WITH firsts AS (
    SELECT customer_id, date_trunc('month', min(placed_at)) AS cohort
    FROM orders WHERE status <> 'cancelled'
    GROUP BY customer_id
),
activity AS (
    SELECT DISTINCT customer_id, date_trunc('month', placed_at) AS month
    FROM orders WHERE status <> 'cancelled'
)
SELECT f.cohort::date AS cohort,
       (extract(year FROM age(a.month, f.cohort)) * 12 + extract(month FROM age(a.month, f.cohort)))::int AS months_later,
       count(*) AS customers
FROM firsts f
JOIN activity a USING (customer_id)
GROUP BY 1, 2
HAVING (extract(year FROM age(a.month, f.cohort)) * 12 + extract(month FROM age(a.month, f.cohort))) <= 12
ORDER BY 1, 2`},
			{Key: KeyDuckDBRaw, SQL: `WITH firsts AS (
    SELECT customer_id, date_trunc('month', min(placed_at)) AS cohort
    FROM raw.orders WHERE status <> 'cancelled'
    GROUP BY customer_id
),
activity AS (
    SELECT DISTINCT customer_id, date_trunc('month', placed_at) AS month
    FROM raw.orders WHERE status <> 'cancelled'
)
SELECT f.cohort::DATE AS cohort,
       date_diff('month', f.cohort, a.month) AS months_later,
       count(*) AS customers
FROM firsts f
JOIN activity a USING (customer_id)
GROUP BY 1, 2
HAVING months_later <= 12
ORDER BY 1, 2`},
			{Key: KeyDuckDBStar, SQL: `WITH firsts AS (
    SELECT customer_id, date_trunc('month', min(order_date)) AS cohort
    FROM dw.fact_orders WHERE status <> 'cancelled'
    GROUP BY ALL
),
activity AS (
    SELECT DISTINCT customer_id, date_trunc('month', order_date) AS month
    FROM dw.fact_orders WHERE status <> 'cancelled'
)
SELECT f.cohort AS cohort,
       date_diff('month', f.cohort, a.month) AS months_later,
       count(*) AS customers
FROM firsts f
JOIN activity a USING (customer_id)
GROUP BY ALL
HAVING months_later <= 12
ORDER BY ALL`},
		},
	},
	{
		ID:       "brand-returns",
		Kind:     KindAnalytics,
		Title:    "Return rate by brand",
		Question: "Units sold vs units returned for every brand, over all delivered orders.",
		Why: "A five-table join over millions of rows with a LEFT JOIN on a composite key. " +
			"This SQL is identical on both engines (only the schema prefix changes): same query, different engine.",
		Variants: []Variant{
			{Key: KeyPostgres, SQL: `SELECT b.name AS brand,
       sum(oi.quantity) AS units_sold,
       coalesce(sum(r.quantity), 0) AS units_returned,
       round(100.0 * coalesce(sum(r.quantity), 0) / sum(oi.quantity), 2) AS return_rate_pct
FROM order_items oi
JOIN orders o ON o.id = oi.order_id AND o.status = 'delivered'
JOIN products p ON p.id = oi.product_id
JOIN brands b ON b.id = p.brand_id
LEFT JOIN returns r ON r.order_id = oi.order_id AND r.line_no = oi.line_no
GROUP BY b.name
HAVING sum(oi.quantity) > 1000
ORDER BY return_rate_pct DESC
LIMIT 15`},
			{Key: KeyDuckDBRaw, SQL: `SELECT b.name AS brand,
       sum(oi.quantity) AS units_sold,
       coalesce(sum(r.quantity), 0) AS units_returned,
       round(100.0 * coalesce(sum(r.quantity), 0) / sum(oi.quantity), 2) AS return_rate_pct
FROM raw.order_items oi
JOIN raw.orders o ON o.id = oi.order_id AND o.status = 'delivered'
JOIN raw.products p ON p.id = oi.product_id
JOIN raw.brands b ON b.id = p.brand_id
LEFT JOIN raw.returns r ON r.order_id = oi.order_id AND r.line_no = oi.line_no
GROUP BY b.name
HAVING sum(oi.quantity) > 1000
ORDER BY return_rate_pct DESC
LIMIT 15`},
			{Key: KeyDuckDBStar, SQL: `SELECT dp.brand,
       sum(fs.quantity) AS units_sold,
       coalesce(sum(fr.quantity), 0) AS units_returned,
       round(100.0 * coalesce(sum(fr.quantity), 0) / sum(fs.quantity), 2) AS return_rate_pct
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
LEFT JOIN dw.fact_returns fr USING (order_id, line_no)
WHERE fs.order_status = 'delivered'
GROUP BY ALL
HAVING units_sold > 1000
ORDER BY return_rate_pct DESC
LIMIT 15`},
		},
	},
	{
		ID:       "order-lookup",
		Kind:     KindLookup,
		Title:    "Show one order with its lines (point lookup)",
		Question: "The query behind an order page: one order id, a handful of rows.",
		Why: "Postgres walks the primary key B-tree straight to the few rows it needs. " +
			"DuckDB has no B-tree on these tables; it relies on min/max zone maps to skip row groups, then scans what is left. " +
			"Both are fast in absolute terms, but this is the kind of query an OLTP database is built for.",
		Args: randomOrderID,
		Variants: []Variant{
			{Key: KeyPostgres, SQL: `SELECT o.id, o.status, o.placed_at, o.total,
       oi.line_no, p.name, oi.quantity, oi.unit_price
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
JOIN products p ON p.id = oi.product_id
WHERE o.id = $1
ORDER BY oi.line_no`},
			{Key: KeyDuckDBRaw, SQL: `SELECT o.id, o.status, o.placed_at, o.total,
       oi.line_no, p.name, oi.quantity, oi.unit_price
FROM raw.orders o
JOIN raw.order_items oi ON oi.order_id = o.id
JOIN raw.products p ON p.id = oi.product_id
WHERE o.id = $1
ORDER BY oi.line_no`},
			{Key: KeyDuckDBStar, SQL: `SELECT fs.order_id, fs.order_status, fs.placed_at,
       fs.line_no, dp.product_name, fs.quantity, fs.unit_price_local
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
WHERE fs.order_id = $1
ORDER BY fs.line_no`},
		},
	},
	{
		ID:       "customer-orders",
		Kind:     KindLookup,
		Title:    "A customer's 10 latest orders",
		Question: "The 'My orders' page: filter by customer, newest first, top 10.",
		Why: "Postgres has an index on orders (customer_id, placed_at DESC): it reads exactly 10 index entries in order and stops. " +
			"DuckDB must check the customer_id column of every row group, because orders are stored by date, not by customer.",
		Args: randomCustomerID,
		Variants: []Variant{
			{Key: KeyPostgres, SQL: `SELECT id, placed_at, status, currency_code, total
FROM orders
WHERE customer_id = $1
ORDER BY placed_at DESC
LIMIT 10`},
			{Key: KeyDuckDBRaw, SQL: `SELECT id, placed_at, status, currency_code, total
FROM raw.orders
WHERE customer_id = $1
ORDER BY placed_at DESC
LIMIT 10`},
			{Key: KeyDuckDBStar, SQL: `SELECT order_id, placed_at, status, currency_code, total_local
FROM dw.fact_orders
WHERE customer_id = $1
ORDER BY placed_at DESC
LIMIT 10`},
		},
	},
	{
		ID:       "single-inserts",
		Kind:     KindWrite,
		Title:    "2,000 single-row INSERTs, each in its own transaction",
		Question: "What an application does all day: one small write, commit, repeat.",
		Why: "Every commit in DuckDB appends to its write-ahead log and updates columnar segments built for bulk data. " +
			"Postgres is designed for exactly this pattern: a row goes into a heap page and the WAL, with commits that cost very little.",
		Variants: []Variant{
			{Key: KeyPostgres, SQL: "INSERT INTO bench.events (id, product_id, quantity, created_at) VALUES ($1, $2, $3, now())", Exec: pgSingleInserts},
			{Key: KeyDuckDBRaw, Label: "DuckDB (scratch file)", SQL: "INSERT INTO events (id, product_id, quantity, created_at) VALUES ($1, $2, $3, now())", Exec: duckSingleInserts},
		},
	},
	{
		ID:       "single-updates",
		Kind:     KindWrite,
		Title:    "2,000 single-row UPDATEs by primary key",
		Question: "Take one unit of stock, commit, repeat.",
		Why: "Postgres finds the row through the primary key index and writes a new row version. " +
			"DuckDB also uses its primary key index to find the row, but an update in a column store rewrites values inside compressed column segments.",
		Variants: []Variant{
			{Key: KeyPostgres, SQL: "UPDATE bench.stock SET quantity = quantity - 1 WHERE product_id = $1", Exec: pgSingleUpdates},
			{Key: KeyDuckDBRaw, Label: "DuckDB (scratch file)", SQL: "UPDATE stock SET quantity = quantity - 1 WHERE product_id = $1", Exec: duckSingleUpdates},
		},
	},
	{
		ID:       "concurrent-updates",
		Kind:     KindWrite,
		Title:    "8 concurrent writers updating the same 16 hot rows",
		Question: "8 goroutines x 150 updates each, all fighting over a few best-selling products.",
		Why: "Postgres locks the row: the second writer WAITS, then continues, and every update succeeds. " +
			"DuckDB uses optimistic concurrency: when two transactions change the same row, one of them FAILS with a conflict error " +
			"and the application must retry. (Across processes it is stricter: only one process can open the file for writing at all.)",
		Variants: []Variant{
			{Key: KeyPostgres, SQL: "UPDATE bench.stock SET quantity = quantity - 1 WHERE product_id = $1", Exec: pgConcurrentUpdates},
			{Key: KeyDuckDBRaw, Label: "DuckDB (scratch file)", SQL: "UPDATE stock SET quantity = quantity - 1 WHERE product_id = $1", Exec: duckConcurrentUpdates},
		},
	},
	{
		ID:       "bulk-insert",
		Kind:     KindWrite,
		Title:    "Bulk load 5 million rows with one INSERT ... SELECT",
		Question: "The ETL-style write: many rows in one statement.",
		Why: "Big batches are what DuckDB writes best: columnar appends, compressed as they go, on all cores. " +
			"So the rule is not 'DuckDB is bad at writes', it is 'DuckDB is bad at MANY SMALL writes'.",
		Variants: []Variant{
			{Key: KeyPostgres, SQL: "INSERT INTO bench.bulk SELECT g, g % 30000, (g % 5) + 1, now() FROM generate_series(1, 5000000) AS g", Exec: pgBulkInsert},
			{Key: KeyDuckDBRaw, Label: "DuckDB (scratch file)", SQL: "INSERT INTO bulk SELECT i, i % 30000, (i % 5) + 1, now() FROM range(1, 5000001) AS t(i)", Exec: duckBulkInsert},
		},
	},
}
