SELECT date_trunc('month', placed_at)::date AS month,
       count(DISTINCT customer_id)         AS active_customers,
       count(*)                            AS orders
FROM store.orders
WHERE status <> 'cancelled'
  AND id <= @max_order_id
GROUP BY 1
ORDER BY 1
