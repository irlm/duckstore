WITH recent AS (
    SELECT DISTINCT order_id, product_id
    FROM dw.fact_sales
    WHERE order_date > DATEADD(day, -90, (SELECT max(order_date) FROM dw.fact_orders))
),
pairs AS (
    SELECT TOP (20) a.product_id AS product_a, b.product_id AS product_b, count(*) AS orders_together
    FROM recent a
    JOIN recent b ON b.order_id = a.order_id AND b.product_id > a.product_id
    GROUP BY a.product_id, b.product_id
    ORDER BY count(*) DESC, a.product_id, b.product_id
)
SELECT p.product_a, pa.product_name AS name_a, p.product_b, pb.product_name AS name_b, p.orders_together
FROM pairs p
JOIN dw.dim_product pa ON pa.product_id = p.product_a AND pa.is_current = 1
JOIN dw.dim_product pb ON pb.product_id = p.product_b AND pb.is_current = 1
ORDER BY p.orders_together DESC, p.product_a, p.product_b
