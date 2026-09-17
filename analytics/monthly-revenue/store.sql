-- Revenue per month from the normalized store tables.
SELECT date_trunc('month', o.placed_at)::date AS month,
       count(DISTINCT o.id)                  AS orders,
       sum(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2)::numeric(14, 2)) AS revenue_usd
FROM store.orders o
JOIN store.order_items oi ON oi.order_id = o.id
JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
WHERE o.status <> 'cancelled'
  AND o.id <= @max_order_id          -- the warehouse watermark: same orders as DuckDB
GROUP BY 1
ORDER BY 1
