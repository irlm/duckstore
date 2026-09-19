-- Each department's last 12 complete months next to the same month a year earlier.
-- Intervals become DATEADD in T-SQL.
WITH tree AS (
    SELECT id,
           CAST(name AS varchar(200)) AS department,
           CAST(name AS varchar(200)) AS level1,
           CAST(NULL AS varchar(200)) AS level2,
           1 AS depth
    FROM store.categories WHERE parent_id IS NULL
    UNION ALL
    SELECT c.id, t.department, t.level1,
           CAST(CASE WHEN t.depth = 1 THEN c.name ELSE t.level2 END AS varchar(200)),
           t.depth + 1
    FROM store.categories c JOIN tree t ON c.parent_id = t.id
),
monthly AS (
    SELECT t.department,
           CAST(DATETRUNC(month, o.placed_at) AS date) AS month,
           sum(CAST(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2) AS decimal(14, 2))) AS revenue
    FROM store.orders o
    JOIN store.order_items oi ON oi.order_id = o.id
    JOIN store.products pr ON pr.id = oi.product_id
    JOIN tree t ON t.id = pr.category_id
    JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = CAST(o.placed_at AS date)
    WHERE o.status <> 'cancelled' AND o.id <= @max_order_id
    GROUP BY t.department, CAST(DATETRUNC(month, o.placed_at) AS date)
),
bounds AS (
    SELECT CAST(DATETRUNC(month, max(placed_at)) AS date) AS current_month FROM store.orders WHERE id <= @max_order_id
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
