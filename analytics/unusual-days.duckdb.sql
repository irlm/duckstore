WITH daily AS (
    SELECT order_date, count(*) AS orders
    FROM dw.fact_orders
    GROUP BY order_date
),
scored AS (
    SELECT order_date, orders,
           avg(orders) OVER (ORDER BY order_date ROWS BETWEEN 7 PRECEDING AND 1 PRECEDING) AS avg_prev_7d
    FROM daily
)
SELECT order_date,
       orders,
       round(avg_prev_7d, 1)                          AS avg_prev_7d,
       round(100.0 * orders / avg_prev_7d - 100, 1)   AS change_pct
FROM scored
WHERE order_date > (SELECT min(order_date) + 30 FROM daily)
  AND (orders < 0.6 * avg_prev_7d OR orders > 1.8 * avg_prev_7d)
ORDER BY order_date
