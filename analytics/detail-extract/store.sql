SELECT oi.order_id, oi.line_no, o.placed_at, o.customer_id, oi.product_id, p.name AS product_name,
       oi.quantity, o.currency_code, oi.unit_price AS unit_price_local,
       round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2)::numeric(14, 2) AS net_usd
FROM store.orders o
JOIN store.order_items oi ON oi.order_id = o.id
JOIN store.products p ON p.id = oi.product_id
JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
WHERE o.status <> 'cancelled'
  AND o.id <= @max_order_id
  -- the newest day that has orders. Not "yesterday": a load test or an import can leave a
  -- gap, and then a fixed offset asks for a day with nothing in it.
  AND o.placed_at >= (SELECT max(placed_at)::date FROM store.orders WHERE id <= @max_order_id)
ORDER BY oi.order_id, oi.line_no
