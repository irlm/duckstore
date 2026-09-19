WITH firsts AS (
    SELECT customer_id, DATETRUNC(month, min(order_date)) AS cohort
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
    GROUP BY customer_id
),
activity AS (
    SELECT DISTINCT customer_id, DATETRUNC(month, order_date) AS month
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
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
