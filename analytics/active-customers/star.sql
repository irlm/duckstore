SELECT date_trunc('month', order_date)::DATE AS month,
       count(DISTINCT customer_id)          AS active_customers,
       count(*)                             AS orders
FROM dw.fact_orders
WHERE status <> 'cancelled'
GROUP BY 1
ORDER BY 1
