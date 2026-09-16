-- Revenue per month from the normalized store tables.
WITH fx_daily AS MATERIALIZED (
    -- Postgres has no ASOF JOIN. Build one exchange rate per currency per calendar
    -- day (weekends carry the last business day's rate), then join on (currency, day).
    SELECT c.code AS currency_code, d::date AS day,
           (SELECT f.units_per_usd
              FROM store.fx_rates f
             WHERE f.currency_code = c.code AND f.rate_date <= d::date
             ORDER BY f.rate_date DESC
             LIMIT 1) AS units_per_usd
    FROM store.currencies c
    CROSS JOIN generate_series((SELECT min(placed_at)::date FROM store.orders),
                               (SELECT max(placed_at)::date FROM store.orders),
                               interval '1 day') AS g(d)
)
SELECT date_trunc('month', o.placed_at)::date AS month,
       count(DISTINCT o.id)                  AS orders,
       sum(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2)::numeric(14, 2)) AS revenue_usd
FROM store.orders o
JOIN store.order_items oi ON oi.order_id = o.id
JOIN fx_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
WHERE o.status <> 'cancelled'
  AND o.id <= @max_order_id          -- the warehouse watermark: same orders as DuckDB
GROUP BY 1
ORDER BY 1
