-- title: 10 · Postgres for SQL Server developers (the OLTP side)
-- engine: postgres
--
-- This lesson runs on POSTGRES, in a read-only transaction.
-- Tick "EXPLAIN ANALYZE each statement" to see which indexes are used.

-- An index seek + top-N: uses orders_customer_placed_idx (customer_id, placed_at DESC).
SELECT id, placed_at, status, total
FROM orders
WHERE customer_id = (SELECT customer_id FROM orders WHERE id = 1000)
ORDER BY placed_at DESC
LIMIT 10;

-- DISTINCT ON: the first row of each group by ORDER BY.
-- (T-SQL: ROW_NUMBER() OVER (PARTITION BY ...) in a CTE, then WHERE rn = 1)
SELECT DISTINCT ON (currency_code) currency_code, rate_date, units_per_usd
FROM fx_rates
ORDER BY currency_code, rate_date DESC;

-- LATERAL = CROSS APPLY: the 3 latest reviews of each of 5 products.
SELECT p.id, p.name, r.rating, r.created_at
FROM (SELECT id, name FROM products ORDER BY id LIMIT 5) p
CROSS JOIN LATERAL (
    SELECT rating, created_at
    FROM reviews
    WHERE product_id = p.id
    ORDER BY created_at DESC
    LIMIT 3
) r;

-- FILTER on an aggregate (T-SQL: SUM(CASE WHEN ... THEN 1 ELSE 0 END)).
SELECT status,
       count(*)                                         AS orders,
       count(*) FILTER (WHERE promotion_id IS NOT NULL) AS with_promotion
FROM orders
WHERE placed_at >= now() - interval '30 days'
GROUP BY status;

-- jsonb: ->> extracts text, @> tests containment.
SELECT id, name, attributes ->> 'color' AS color, attributes -> 'sizes' AS sizes
FROM products
WHERE attributes @> '{"color": "red", "warranty_months": 24}'
LIMIT 5;

-- generate_series makes a calendar without a numbers table.
SELECT d::date AS day, count(o.id) AS orders
FROM generate_series(current_date - 6, current_date, interval '1 day') AS d
LEFT JOIN orders o ON o.placed_at >= d AND o.placed_at < d + interval '1 day'
GROUP BY d
ORDER BY d;

-- Table and index sizes (T-SQL: sp_spaceused).
SELECT c.relname                                            AS name,
       CASE c.relkind WHEN 'r' THEN 'table' ELSE 'index' END AS kind,
       pg_size_pretty(pg_relation_size(c.oid))              AS size
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'store' AND c.relkind IN ('r', 'i')
ORDER BY pg_relation_size(c.oid) DESC
LIMIT 15;

-- Which indexes are really used? (T-SQL: sys.dm_db_index_usage_stats)
SELECT relname AS table_name, indexrelname AS index_name, idx_scan AS scans
FROM pg_stat_user_indexes
WHERE schemaname = 'store'
ORDER BY idx_scan DESC
LIMIT 15;

-- An analytic question on the row store: watch its time in the panel below,
-- then run the same question on DuckDB (switch the engine, use dw.fact_orders).
SELECT date_trunc('year', placed_at)::date AS year,
       count(*)                            AS orders,
       count(DISTINCT customer_id)         AS customers
FROM orders
GROUP BY 1
ORDER BY 1;
