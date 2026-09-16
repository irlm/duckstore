WITH per_customer AS (
    SELECT customer_id,
           (SELECT max(order_date) FROM dw.fact_orders) - max(order_date) AS recency_days,
           count(*)       AS frequency,
           sum(total_usd) AS monetary
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
    GROUP BY customer_id
),
scored AS (
    SELECT *,
           ntile(5) OVER (ORDER BY recency_days DESC, customer_id) AS r,  -- 5 = bought recently
           ntile(5) OVER (ORDER BY frequency, customer_id)         AS f   -- 5 = buys often
    FROM per_customer
)
SELECT CASE WHEN r >= 4 AND f >= 4 THEN 'Champions'
            WHEN f >= 4            THEN 'Loyal'
            WHEN r <= 2 AND f >= 3 THEN 'At risk'
            WHEN r >= 4            THEN 'Recent, occasional'
            WHEN r <= 2            THEN 'Hibernating'
            ELSE 'Needs attention' END  AS segment,
       count(*)                          AS customers,
       round(avg(frequency), 2)          AS avg_orders,
       round(avg(monetary), 2)           AS avg_spend_usd
FROM scored
GROUP BY segment
ORDER BY customers DESC, segment
