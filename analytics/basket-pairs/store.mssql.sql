-- Products bought together in the last 90 days: a self-join of the order lines.
-- LIMIT becomes TOP, which a CTE allows together with ORDER BY.
WITH recent AS (
    SELECT DISTINCT oi.order_id, oi.product_id
    FROM store.order_items oi
    JOIN store.orders o ON o.id = oi.order_id
    WHERE o.status <> 'cancelled'
      AND o.id <= @max_order_id
      AND CAST(o.placed_at AS date) > DATEADD(day, -90, (SELECT max(CAST(placed_at AS date)) FROM store.orders WHERE id <= @max_order_id))
),
pairs AS (
    SELECT TOP (20) a.product_id AS product_a, b.product_id AS product_b, count(*) AS orders_together
    FROM recent a
    JOIN recent b ON b.order_id = a.order_id AND b.product_id > a.product_id
    GROUP BY a.product_id, b.product_id
    ORDER BY count(*) DESC, a.product_id, b.product_id
)
SELECT p.product_a, pa.name AS name_a, p.product_b, pb.name AS name_b, p.orders_together
FROM pairs p
JOIN store.products pa ON pa.id = p.product_a
JOIN store.products pb ON pb.id = p.product_b
ORDER BY p.orders_together DESC, p.product_a, p.product_b
