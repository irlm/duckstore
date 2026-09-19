-- Revenue of every sales leader, rolled up through the org chart.
-- T-SQL has no arrays: the chain of ids is kept as a string like /1/7/23/ and a leader is
-- "above" an employee when that string contains /<leader id>/.
WITH chain AS (
    SELECT id AS employee_id, 0 AS level,
           CAST('/' + CAST(id AS varchar(20)) + '/' AS varchar(400)) AS path
    FROM store.employees WHERE manager_id IS NULL
    UNION ALL
    SELECT e.id, c.level + 1,
           CAST(c.path + CAST(e.id AS varchar(20)) + '/' AS varchar(400))
    FROM store.employees e JOIN chain c ON e.manager_id = c.employee_id
),
customer_revenue AS (
    SELECT c.account_manager_id,
           sum(CAST(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2) AS decimal(14, 2))) AS revenue
    FROM store.orders o
    JOIN store.order_items oi ON oi.order_id = o.id
    JOIN store.customers c ON c.id = o.customer_id
    JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = CAST(o.placed_at AS date)
    WHERE c.account_manager_id IS NOT NULL
      AND o.status <> 'cancelled'
      AND o.id <= @max_order_id
      AND CAST(o.placed_at AS date) > DATEADD(day, -365, (SELECT max(CAST(placed_at AS date)) FROM store.orders WHERE id <= @max_order_id))
    GROUP BY c.account_manager_id
)
SELECT leader.level,
       leader.employee_id,
       e.first_name + ' ' + e.last_name AS name,
       e.title,
       count(DISTINCT ae.employee_id)   AS account_executives,
       sum(cr.revenue)                  AS revenue_usd
FROM chain leader
JOIN store.employees e ON e.id = leader.employee_id
JOIN chain ae ON ae.path LIKE '%/' + CAST(leader.employee_id AS varchar(20)) + '/%'
JOIN customer_revenue cr ON cr.account_manager_id = ae.employee_id
WHERE e.department = 'Sales' AND leader.level <= 3
GROUP BY leader.level, leader.employee_id, e.first_name, e.last_name, e.title
ORDER BY leader.level, revenue_usd DESC, leader.employee_id
