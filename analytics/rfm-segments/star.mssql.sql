WITH per_customer AS (
    SELECT customer_id,
           DATEDIFF(day, max(order_date), (SELECT max(order_date) FROM dw.fact_orders)) AS recency_days,
           count(*)       AS frequency,
           sum(total_usd) AS monetary
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
    GROUP BY customer_id
),
scored AS (
    SELECT customer_id, recency_days, frequency, monetary,
           ntile(5) OVER (ORDER BY recency_days DESC, customer_id) AS r,  -- 5 = bought recently
           ntile(5) OVER (ORDER BY frequency, customer_id)         AS f   -- 5 = buys often
    FROM per_customer
),
segments AS (
    SELECT CASE WHEN r >= 4 AND f >= 4 THEN 'Champions'
                WHEN f >= 4            THEN 'Loyal'
                WHEN r <= 2 AND f >= 3 THEN 'At risk'
                WHEN r >= 4            THEN 'Recent, occasional'
                WHEN r <= 2            THEN 'Hibernating'
                ELSE 'Needs attention' END AS segment,
           frequency, monetary
    FROM scored
)
SELECT segment,
       count(*)                                        AS customers,
       round(avg(CAST(frequency AS decimal(19, 6))), 2) AS avg_orders,
       round(avg(CAST(monetary AS decimal(19, 6))), 2)  AS avg_spend_usd
FROM segments
GROUP BY segment
ORDER BY customers DESC, segment
