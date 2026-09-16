SELECT o.id AS order_id,
       o.placed_at,
       o.status,
       (SELECT count(*) FROM store.order_items oi WHERE oi.order_id = o.id) AS line_count,
       round(o.total / fx.units_per_usd, 2)::numeric(14, 2)                                 AS total_usd
FROM store.orders o
CROSS JOIN LATERAL (            -- 20 rows: a lookup per row is cheap here
    SELECT f.units_per_usd
    FROM store.fx_rates f
    WHERE f.currency_code = o.currency_code AND f.rate_date <= o.placed_at::date
    ORDER BY f.rate_date DESC
    LIMIT 1
) fx
WHERE o.customer_id = @customer_id
  AND o.id <= @max_order_id
ORDER BY o.placed_at DESC, o.id DESC
LIMIT 20
