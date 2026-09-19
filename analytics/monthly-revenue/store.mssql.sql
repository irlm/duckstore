-- Revenue per month from the normalized store tables (T-SQL).
-- Differences from the Postgres file: DATETRUNC instead of date_trunc, CAST instead of ::,
-- and the grouping expression repeated, because T-SQL cannot GROUP BY an ordinal.
SELECT CAST(DATETRUNC(month, o.placed_at) AS date) AS month,
       count(DISTINCT o.id)                        AS orders,
       sum(CAST(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2) AS decimal(14, 2))) AS revenue_usd
FROM store.orders o
JOIN store.order_items oi ON oi.order_id = o.id
JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = CAST(o.placed_at AS date)
WHERE o.status <> 'cancelled'
  AND o.id <= @max_order_id          -- the warehouse watermark: same orders as DuckDB
GROUP BY CAST(DATETRUNC(month, o.placed_at) AS date)
ORDER BY 1
