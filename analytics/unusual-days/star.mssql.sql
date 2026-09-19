-- Days far below or above the previous 7 days. AVG over an int column returns an int in
-- T-SQL, so the count is cast to decimal first; Postgres returns numeric on its own.
WITH daily AS (
    SELECT order_date, count(*) AS orders
    FROM dw.fact_orders
    GROUP BY order_date
),
scored AS (
    SELECT order_date, orders,
           avg(CAST(orders AS decimal(19, 6))) OVER (ORDER BY order_date ROWS BETWEEN 7 PRECEDING AND 1 PRECEDING) AS avg_prev_7d
    FROM daily
)
SELECT order_date,
       orders,
       round(avg_prev_7d, 1)                        AS avg_prev_7d,
       round(100.0 * orders / avg_prev_7d - 100, 1) AS change_pct
FROM scored
WHERE order_date > (SELECT DATEADD(day, 30, min(order_date)) FROM daily)
  AND (orders < 0.6 * avg_prev_7d OR orders > 1.8 * avg_prev_7d)
ORDER BY order_date
