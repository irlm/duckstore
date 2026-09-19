SELECT CAST(DATETRUNC(month, order_date) AS date) AS month,
       count(DISTINCT customer_id)                AS active_customers,
       count(*)                                   AS orders
FROM dw.fact_orders
WHERE status <> 'cancelled'
GROUP BY CAST(DATETRUNC(month, order_date) AS date)
ORDER BY 1
