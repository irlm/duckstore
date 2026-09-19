-- Customers who first bought in month X, buying again N months later.
-- T-SQL has DATEDIFF(month, a, b), which counts month boundaries like the year/month
-- arithmetic the Postgres file does by hand.
WITH firsts AS (
    SELECT customer_id, DATETRUNC(month, min(placed_at)) AS cohort
    FROM store.orders
    WHERE status <> 'cancelled' AND id <= @max_order_id
    GROUP BY customer_id
),
activity AS (
    SELECT DISTINCT customer_id, DATETRUNC(month, placed_at) AS month
    FROM store.orders
    WHERE status <> 'cancelled' AND id <= @max_order_id
),
pairs AS (
    SELECT f.cohort, DATEDIFF(month, f.cohort, a.month) AS months_later
    FROM firsts f
    JOIN activity a ON a.customer_id = f.customer_id
)
SELECT CAST(cohort AS date) AS cohort, months_later, count(*) AS customers
FROM pairs
WHERE months_later <= 12
GROUP BY CAST(cohort AS date), months_later
ORDER BY 1, 2
