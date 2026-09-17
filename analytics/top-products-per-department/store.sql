WITH RECURSIVE tree AS (
    -- walk the category tree down from each department (top-level category)
    SELECT id, name AS department, name AS level1, NULL::text AS level2, 1 AS depth
    FROM store.categories WHERE parent_id IS NULL
    UNION ALL
    SELECT c.id, t.department, t.level1, CASE WHEN t.depth = 1 THEN c.name ELSE t.level2 END, t.depth + 1
    FROM store.categories c JOIN tree t ON c.parent_id = t.id
),
revenue AS (
    SELECT t.department, p.id AS product_id, p.name AS product_name,
           sum(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2)::numeric(14, 2)) AS revenue_usd
    FROM store.orders o
    JOIN store.order_items oi ON oi.order_id = o.id
    JOIN store.products p ON p.id = oi.product_id
    JOIN tree t ON t.id = p.category_id
    JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
    WHERE o.status <> 'cancelled'
      AND o.id <= @max_order_id
      AND o.placed_at::date > (SELECT max(placed_at)::date FROM store.orders WHERE id <= @max_order_id) - 365
    GROUP BY t.department, p.id, p.name
)
SELECT department, rank, product_id, product_name, revenue_usd
FROM (
    SELECT *, row_number() OVER (PARTITION BY department ORDER BY revenue_usd DESC, product_id) AS rank
    FROM revenue
) ranked
WHERE rank <= 3                     -- no QUALIFY in Postgres: filter in an outer query
ORDER BY department, rank
