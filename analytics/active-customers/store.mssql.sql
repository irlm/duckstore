SELECT CAST(DATETRUNC(month, placed_at) AS date) AS month,
       count(DISTINCT customer_id)               AS active_customers,
       count(*)                                  AS orders
FROM store.orders
WHERE status <> 'cancelled'
  AND id <= @max_order_id
GROUP BY CAST(DATETRUNC(month, placed_at) AS date)
ORDER BY 1
