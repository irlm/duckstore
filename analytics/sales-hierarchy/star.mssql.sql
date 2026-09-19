-- The warehouse stored each manager chain as a list. SQL Server has no list type, so the
-- loader wrote it as JSON text and OPENJSON reads it back — the T-SQL way of asking
-- "is this leader anywhere above that employee?".
WITH customer_revenue AS (
    SELECT dc.account_manager_id, sum(fs.net_usd) AS revenue
    FROM dw.fact_sales fs
    JOIN dw.dim_customer dc ON dc.customer_id = fs.customer_id
    WHERE dc.account_manager_id IS NOT NULL
      AND fs.order_date > DATEADD(day, -365, (SELECT max(order_date) FROM dw.fact_orders))
    GROUP BY dc.account_manager_id
)
SELECT leader.level,
       leader.employee_id,
       leader.name,
       leader.title,
       count(DISTINCT ae.employee_id) AS account_executives,
       sum(cr.revenue)                AS revenue_usd
FROM dw.dim_employee leader
JOIN dw.dim_employee ae
  ON EXISTS (SELECT 1 FROM OPENJSON(ae.management_chain_ids) WHERE CAST(value AS bigint) = leader.employee_id)
JOIN customer_revenue cr ON cr.account_manager_id = ae.employee_id
WHERE leader.department = 'Sales' AND leader.level <= 3
GROUP BY leader.level, leader.employee_id, leader.name, leader.title
ORDER BY leader.level, revenue_usd DESC, leader.employee_id
