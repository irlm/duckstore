WITH RECURSIVE tree AS (
    -- walk the category tree down from each department (top-level category)
    SELECT id, name AS department, name AS level1, NULL::text AS level2, 1 AS depth
    FROM store.categories WHERE parent_id IS NULL
    UNION ALL
    SELECT c.id, t.department, t.level1, CASE WHEN t.depth = 1 THEN c.name ELSE t.level2 END, t.depth + 1
    FROM store.categories c JOIN tree t ON c.parent_id = t.id
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
monthly AS (
    SELECT t.department,
           date_trunc('month', o.placed_at)::date AS month,
           sum(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2)) AS revenue
    FROM store.orders o
    JOIN store.order_items oi ON oi.order_id = o.id
    JOIN store.products pr ON pr.id = oi.product_id
    JOIN tree t ON t.id = pr.category_id
    JOIN fx_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
    WHERE o.status <> 'cancelled' AND o.id <= @max_order_id
    GROUP BY 1, 2
),
bounds AS (
    SELECT date_trunc('month', max(placed_at))::date AS current_month FROM store.orders WHERE id <= @max_order_id
)
SELECT m.department,
       m.month,
       m.revenue                                     AS revenue_usd,
       p.revenue                                     AS revenue_prev_year_usd,
       round(100.0 * (m.revenue / p.revenue - 1), 1) AS growth_pct
FROM monthly m
JOIN bounds b ON m.month >= b.current_month - interval '12 months' AND m.month < b.current_month
LEFT JOIN monthly p ON p.department = m.department AND p.month = (m.month - interval '12 months')::date
ORDER BY m.department, m.month
