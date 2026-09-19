-- Every order line of the latest complete day: a large result, to show the transfer cost.
SELECT oi.order_id, oi.line_no, o.placed_at, o.customer_id, oi.product_id, p.name AS product_name,
       oi.quantity, o.currency_code, oi.unit_price AS unit_price_local,
       CAST(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2) AS decimal(14, 2)) AS net_usd
FROM store.orders o
JOIN store.order_items oi ON oi.order_id = o.id
JOIN store.products p ON p.id = oi.product_id
JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = CAST(o.placed_at AS date)
WHERE o.status <> 'cancelled'
  AND o.id <= @max_order_id
  AND o.placed_at >= DATEADD(day, -1, (SELECT CAST(max(placed_at) AS date) FROM store.orders WHERE id <= @max_order_id))
  AND o.placed_at <  (SELECT CAST(max(placed_at) AS date) FROM store.orders WHERE id <= @max_order_id)
ORDER BY oi.order_id, oi.line_no
