-- title: 04 · Time: ASOF joins, SCD Type 2 and missing days
-- engine: duckdb
--
-- Three time problems every warehouse has, and the DuckDB tools for them.

-- 1. ASOF JOIN: exchange rates exist only on business days. For each order,
--    take the latest rate on or before the order date. Weekend orders get
--    Friday's rate.
SELECT o.id,
       o.placed_at::DATE     AS order_date,
       dayname(o.placed_at)  AS order_day,
       fx.rate_date,
       dayname(fx.rate_date) AS rate_day,
       fx.units_per_usd
FROM raw.orders o
ASOF JOIN raw.fx_rates fx
       ON fx.currency_code = o.currency_code
      AND o.placed_at::DATE >= fx.rate_date
WHERE o.currency_code = 'EUR'
  AND isodow(o.placed_at) IN (6, 7)
ORDER BY o.id
LIMIT 6;

-- The T-SQL way (OUTER APPLY + TOP 1) written as LATERAL also works in DuckDB.
-- On 2.5M orders the ASOF JOIN is the tool built for the job.
SELECT o.id, fx.rate_date, fx.units_per_usd
FROM (SELECT id, currency_code, placed_at FROM raw.orders WHERE currency_code = 'EUR' ORDER BY id LIMIT 6) o,
LATERAL (
    SELECT f.rate_date, f.units_per_usd
    FROM raw.fx_rates f
    WHERE f.currency_code = o.currency_code
      AND f.rate_date <= o.placed_at::DATE
    ORDER BY f.rate_date DESC
    LIMIT 1
) fx
ORDER BY o.id;

-- 2. SCD Type 2: dim_product has one row per price VERSION. Each sale points
--    to the version that was valid when it happened (product_key), so old
--    sales keep their old list price. Here: price then vs price today.
WITH old_version_sales AS (
    SELECT then_.product_key,
           then_.product_name,
           then_.list_price_usd AS list_price_then,
           now_.list_price_usd  AS list_price_today,
           count(*)             AS lines_sold_at_old_price
    FROM dw.fact_sales fs
    JOIN dw.dim_product then_ ON then_.product_key = fs.product_key
    JOIN dw.dim_product now_  ON now_.product_id = fs.product_id AND now_.is_current
    WHERE NOT then_.is_current
    GROUP BY ALL
)
SELECT product_name,
       lines_sold_at_old_price,
       list_price_then,
       list_price_today,
       round(100.0 * (list_price_today / list_price_then - 1), 1) AS change_pct
FROM old_version_sales
ORDER BY abs(change_pct) DESC
LIMIT 10;

-- A point-in-time question: what did the catalog look like on one date?
-- (SQL Server temporal tables: FOR SYSTEM_TIME AS OF)
SELECT category_l1                  AS department,
       count(*)                     AS products,
       round(avg(list_price_usd), 2) AS avg_list_price_usd
FROM dw.dim_product
WHERE TIMESTAMP '2025-01-01' >= valid_from
  AND TIMESTAMP '2025-01-01' <  valid_to
GROUP BY ALL
ORDER BY ALL;

-- 3. Missing days: a day without sales has no row in a fact table. Generate
--    the calendar and LEFT JOIN, so the zeros appear.
WITH product AS (
    SELECT product_id FROM dw.product_stats WHERE units_90d BETWEEN 5 AND 15 ORDER BY product_id LIMIT 1
),
days AS (
    SELECT unnest(generate_series((SELECT max(order_date) FROM dw.fact_sales) - INTERVAL 13 DAY,
                                  (SELECT max(order_date) FROM dw.fact_sales),
                                  INTERVAL 1 DAY))::DATE AS day
)
SELECT d.day,
       dayname(d.day)               AS weekday,
       coalesce(sum(fs.quantity), 0) AS units_sold
FROM days d
LEFT JOIN dw.fact_sales fs
       ON fs.order_date = d.day
      AND fs.product_id = (SELECT product_id FROM product)
GROUP BY ALL
ORDER BY d.day;

-- time_bucket: group timestamps into fixed-size buckets (weeks here).
SELECT time_bucket(INTERVAL 1 WEEK, placed_at) AS week_starting,
       count(*)                               AS orders
FROM dw.fact_orders
GROUP BY ALL
ORDER BY week_starting DESC
LIMIT 8;
