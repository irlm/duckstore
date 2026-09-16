-- Postgres: id = ANY (array) instead of DuckDB's list_contains(list, id).
WITH customer_revenue AS (
    SELECT dc.account_manager_id, sum(fs.net_usd) AS revenue
    FROM dw.fact_sales fs
    JOIN dw.dim_customer dc USING (customer_id)
    WHERE dc.account_manager_id IS NOT NULL
      AND fs.order_date > (SELECT max(order_date) FROM dw.fact_orders) - 365
    GROUP BY dc.account_manager_id
)
SELECT leader.level,
       leader.employee_id,
       leader.name,
       leader.title,
       count(DISTINCT ae.employee_id) AS account_executives,
       sum(cr.revenue)                AS revenue_usd
FROM dw.dim_employee leader
JOIN dw.dim_employee ae ON leader.employee_id = ANY (ae.management_chain_ids)  -- everyone below
JOIN customer_revenue cr ON cr.account_manager_id = ae.employee_id
WHERE leader.department = 'Sales' AND leader.level <= 3
GROUP BY leader.level, leader.employee_id, leader.name, leader.title
ORDER BY leader.level, revenue_usd DESC, leader.employee_id
