WITH monthly AS (
    SELECT dp.category_l1 AS department,
           date_trunc('month', fs.order_date)::DATE AS month,
           sum(fs.net_usd) AS revenue
    FROM dw.fact_sales fs
    JOIN dw.dim_product dp USING (product_key)
    GROUP BY 1, 2
),
bounds AS (
    SELECT date_trunc('month', max(order_date))::DATE AS current_month FROM dw.fact_orders
)
SELECT m.department,
       m.month,
       m.revenue                                   AS revenue_usd,
       p.revenue                                   AS revenue_prev_year_usd,
       round(100.0 * (m.revenue / p.revenue - 1), 1) AS growth_pct
FROM monthly m
JOIN bounds b ON m.month >= b.current_month - INTERVAL 12 MONTH AND m.month < b.current_month
LEFT JOIN monthly p ON p.department = m.department AND p.month = (m.month - INTERVAL 12 MONTH)::DATE
ORDER BY m.department, m.month
