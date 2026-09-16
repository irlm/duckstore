WITH RECURSIVE chain AS (
    -- every employee with the path of ids from the CEO down to them
    SELECT id AS employee_id, 0 AS level, ARRAY[id] AS path
    FROM store.employees WHERE manager_id IS NULL
    UNION ALL
    SELECT e.id, c.level + 1, c.path || e.id
    FROM store.employees e JOIN chain c ON e.manager_id = c.employee_id
),
fx_daily AS MATERIALIZED (
    -- Postgres has no ASOF JOIN. Build one exchange rate per currency per calendar
    -- day (weekends carry the last business day's rate), then join on (currency, day).
    SELECT c.code AS currency_code, d::date AS day,
           (SELECT f.units_per_usd
              FROM store.fx_rates f
             WHERE f.currency_code = c.code AND f.rate_date <= d::date
             ORDER BY f.rate_date DESC
             LIMIT 1) AS units_per_usd
    FROM store.currencies c
    CROSS JOIN generate_series((SELECT min(placed_at)::date FROM store.orders),
                               (SELECT max(placed_at)::date FROM store.orders),
                               interval '1 day') AS d
),
customer_revenue AS (
    SELECT c.account_manager_id,
           sum(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2)) AS revenue
    FROM store.orders o
    JOIN store.order_items oi ON oi.order_id = o.id
    JOIN store.customers c ON c.id = o.customer_id
    JOIN fx_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
    WHERE c.account_manager_id IS NOT NULL
      AND o.status <> 'cancelled'
      AND o.id <= @max_order_id
      AND o.placed_at::date > (SELECT max(placed_at)::date FROM store.orders WHERE id <= @max_order_id) - 365
    GROUP BY c.account_manager_id
)
SELECT leader.level,
       leader.employee_id,
       e.first_name || ' ' || e.last_name AS name,
       e.title,
       count(DISTINCT ae.employee_id)     AS account_executives,
       sum(cr.revenue)                    AS revenue_usd
FROM chain leader
JOIN store.employees e ON e.id = leader.employee_id
JOIN chain ae ON leader.employee_id = ANY (ae.path)
JOIN customer_revenue cr ON cr.account_manager_id = ae.employee_id
WHERE e.department = 'Sales' AND leader.level <= 3
GROUP BY leader.level, leader.employee_id, e.first_name, e.last_name, e.title
ORDER BY leader.level, revenue_usd DESC, leader.employee_id
