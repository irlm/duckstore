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
                               interval '1 day') AS d
)
SELECT oi.order_id, oi.line_no, o.placed_at, o.customer_id, oi.product_id, p.name AS product_name,
       oi.quantity, o.currency_code, oi.unit_price AS unit_price_local,
       round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2) AS net_usd
FROM store.orders o
JOIN store.order_items oi ON oi.order_id = o.id
JOIN store.products p ON p.id = oi.product_id
JOIN fx_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
WHERE o.status <> 'cancelled'
  AND o.id <= @max_order_id
  AND o.placed_at >= (SELECT max(placed_at)::date - 1 FROM store.orders WHERE id <= @max_order_id)
  AND o.placed_at <  (SELECT max(placed_at)::date     FROM store.orders WHERE id <= @max_order_id)
ORDER BY oi.order_id, oi.line_no
