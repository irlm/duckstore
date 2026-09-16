WITH firsts AS (
    SELECT customer_id, date_trunc('month', min(order_date)) AS cohort
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
    GROUP BY customer_id
),
activity AS (
    SELECT DISTINCT customer_id, date_trunc('month', order_date) AS month
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
),
pairs AS (
    SELECT f.cohort, date_diff('month', f.cohort, a.month) AS months_later
    FROM firsts f
    JOIN activity a USING (customer_id)
)
SELECT cohort::DATE AS cohort, months_later, count(*) AS customers
FROM pairs
WHERE months_later <= 12
GROUP BY 1, 2
ORDER BY 1, 2
