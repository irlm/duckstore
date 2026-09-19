SELECT TOP (15)
       b.name                                                             AS brand,
       sum(oi.quantity)                                                   AS units_sold,
       coalesce(sum(r.quantity), 0)                                       AS units_returned,
       round(100.0 * coalesce(sum(r.quantity), 0) / sum(oi.quantity), 2)  AS return_rate_pct
FROM store.order_items oi
JOIN store.orders o ON o.id = oi.order_id AND o.status = 'delivered' AND o.id <= @max_order_id
JOIN store.products p ON p.id = oi.product_id
JOIN store.brands b ON b.id = p.brand_id
LEFT JOIN store.returns r ON r.order_id = oi.order_id AND r.line_no = oi.line_no
GROUP BY b.name
HAVING sum(oi.quantity) > 1000
ORDER BY return_rate_pct DESC, b.name
