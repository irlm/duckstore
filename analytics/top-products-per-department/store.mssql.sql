-- Top 3 products by revenue inside each department, over the last 12 months.
-- The recursive CTE needs no RECURSIVE keyword in T-SQL, but the anchor and the recursive
-- part must have exactly the same column types, hence the CASTs.
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
revenue AS (
    SELECT t.department, p.id AS product_id, p.name AS product_name,
           sum(CAST(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2) AS decimal(14, 2))) AS revenue_usd
    FROM store.orders o
    JOIN store.order_items oi ON oi.order_id = o.id
    JOIN store.products p ON p.id = oi.product_id
    JOIN tree t ON t.id = p.category_id
    JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = CAST(o.placed_at AS date)
    WHERE o.status <> 'cancelled'
      AND o.id <= @max_order_id
      AND CAST(o.placed_at AS date) > DATEADD(day, -365, (SELECT max(CAST(placed_at AS date)) FROM store.orders WHERE id <= @max_order_id))
    GROUP BY t.department, p.id, p.name
)
SELECT department, [rank], product_id, product_name, revenue_usd
FROM (
    SELECT department, product_id, product_name, revenue_usd,
           row_number() OVER (PARTITION BY department ORDER BY revenue_usd DESC, product_id) AS [rank]
    FROM revenue
) ranked
WHERE [rank] <= 3
ORDER BY department, [rank]
