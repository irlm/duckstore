-- title: 09 · Querying live Postgres from DuckDB
-- engine: duckdb
--
-- The server attached the store when it started:
--     ATTACH 'postgres://...' AS pg (TYPE postgres, READ_ONLY);
-- Postgres tables now look like local tables named pg.store.<table>.
-- SQL Server analogy: a linked server, with four-part names.

-- A live count, straight from Postgres.
SELECT count(*) AS orders_in_postgres FROM pg.store.orders;

-- Filters with constants are pushed down: Postgres filters, and only the
-- matching rows cross the connection. (Try it with EXPLAIN ANALYZE.)
SELECT id, placed_at, status, total
FROM pg.store.orders
WHERE id BETWEEN 1000 AND 1010
ORDER BY id;

-- Join live OLTP rows with warehouse facts: stock right now vs sales speed.
WITH stock AS (
    SELECT product_id, sum(quantity_on_hand) AS stock_now
    FROM pg.store.inventory              -- live Postgres rows
    GROUP BY product_id
)
SELECT ps.product_id,
       dp.product_name,
       ps.units_90d,
       s.stock_now,
       round(s.stock_now / (ps.units_90d / 90.0)) AS days_of_stock_left
FROM dw.product_stats ps                  -- warehouse facts
JOIN dw.dim_product dp ON dp.product_id = ps.product_id AND dp.is_current
JOIN stock s           ON s.product_id = ps.product_id
WHERE ps.units_90d > 0
ORDER BY days_of_stock_left
LIMIT 10;

-- Hybrid query: the warehouse has history up to the last ETL; newer orders
-- exist only in Postgres. UNION ALL both for an up-to-the-second total.
-- Note: the subquery on dw.etl_info is NOT pushed down to Postgres (it is not
-- a constant), so every order row is read. The dashboard avoids this by
-- putting the watermark into the SQL as a literal number.
SELECT 'warehouse (last ETL)' AS source, count(*) AS orders, round(sum(total_usd)) AS revenue_usd
FROM dw.fact_orders
UNION ALL
SELECT 'postgres (since ETL)', count(*), round(sum(o.total / fx.units_per_usd))
FROM pg.store.orders o
ASOF JOIN raw.fx_rates fx
       ON fx.currency_code = o.currency_code
      AND o.placed_at::DATE >= fx.rate_date
WHERE o.id > (SELECT max_order_id FROM dw.etl_info);

-- postgres_query sends SQL text to Postgres unchanged (SQL Server: OPENQUERY).
-- Postgres plans it with its own indexes; DuckDB only receives the result.
SELECT *
FROM postgres_query('pg', $$
    SELECT o.id, o.placed_at, o.total, o.currency_code
    FROM store.orders o
    WHERE o.customer_id = (SELECT customer_id FROM store.orders WHERE id = 1000)
    ORDER BY o.placed_at DESC
    LIMIT 5
$$);
