WITH recent AS (
    SELECT DISTINCT order_id, product_id
    FROM dw.fact_sales
    WHERE order_date > (SELECT max(order_date) FROM dw.fact_orders) - 90
),
pairs AS (
    SELECT a.product_id AS product_a, b.product_id AS product_b, count(*) AS orders_together
    FROM recent a
    JOIN recent b ON b.order_id = a.order_id AND b.product_id > a.product_id
    GROUP BY 1, 2
    ORDER BY orders_together DESC, product_a, product_b
    LIMIT 20
)
SELECT p.product_a, pa.product_name AS name_a, p.product_b, pb.product_name AS name_b, p.orders_together
FROM pairs p
JOIN dw.dim_product pa ON pa.product_id = p.product_a AND pa.is_current
JOIN dw.dim_product pb ON pb.product_id = p.product_b AND pb.is_current
ORDER BY p.orders_together DESC, p.product_a, p.product_b
