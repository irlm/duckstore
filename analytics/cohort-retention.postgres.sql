WITH firsts AS (
    SELECT customer_id, date_trunc('month', min(placed_at)) AS cohort
    FROM store.orders
    WHERE status <> 'cancelled' AND id <= @max_order_id
    GROUP BY customer_id
),
activity AS (
    SELECT DISTINCT customer_id, date_trunc('month', placed_at) AS month
    FROM store.orders
    WHERE status <> 'cancelled' AND id <= @max_order_id
),
pairs AS (
    SELECT f.cohort,
           (extract(year FROM age(a.month, f.cohort)) * 12 + extract(month FROM age(a.month, f.cohort)))::int AS months_later
    FROM firsts f
    JOIN activity a USING (customer_id)
)
SELECT cohort::date AS cohort, months_later, count(*) AS customers
FROM pairs
WHERE months_later <= 12
GROUP BY 1, 2
ORDER BY 1, 2
