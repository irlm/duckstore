WITH monthly AS (
    SELECT dp.category_l1 AS department,
           CAST(DATETRUNC(month, fs.order_date) AS date) AS month,
           sum(fs.net_usd) AS revenue
    FROM dw.fact_sales fs
    JOIN dw.dim_product dp ON dp.product_key = fs.product_key
    GROUP BY dp.category_l1, CAST(DATETRUNC(month, fs.order_date) AS date)
),
bounds AS (
    SELECT CAST(DATETRUNC(month, max(order_date)) AS date) AS current_month FROM dw.fact_orders
)
SELECT m.department,
       m.month,
       m.revenue                                     AS revenue_usd,
       p.revenue                                     AS revenue_prev_year_usd,
       round(100.0 * (m.revenue / p.revenue - 1), 1) AS growth_pct
FROM monthly m
JOIN bounds b ON m.month >= DATEADD(month, -12, b.current_month) AND m.month < b.current_month
LEFT JOIN monthly p ON p.department = m.department AND p.month = DATEADD(month, -12, m.month)
ORDER BY m.department, m.month
