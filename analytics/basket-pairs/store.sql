WITH recent AS (
    SELECT DISTINCT oi.order_id, oi.product_id
    FROM store.order_items oi
    JOIN store.orders o ON o.id = oi.order_id
    WHERE o.status <> 'cancelled'
      AND o.id <= @max_order_id
      AND o.placed_at::date > (SELECT max(placed_at)::date FROM store.orders WHERE id <= @max_order_id) - 90
),
pairs AS (
    SELECT a.product_id AS product_a, b.product_id AS product_b, count(*) AS orders_together
    FROM recent a
    JOIN recent b ON b.order_id = a.order_id AND b.product_id > a.product_id
    GROUP BY 1, 2
    ORDER BY orders_together DESC, product_a, product_b
    LIMIT 20
)
SELECT p.product_a, pa.name AS name_a, p.product_b, pb.name AS name_b, p.orders_together
FROM pairs p
JOIN store.products pa ON pa.id = p.product_a
JOIN store.products pb ON pb.id = p.product_b
ORDER BY p.orders_together DESC, p.product_a, p.product_b
