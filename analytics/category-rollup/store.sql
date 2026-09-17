-- Subtotals per department and a grand total with ROLLUP, walking the category tree.
WITH RECURSIVE tree AS (
    -- walk the category tree down from each department (top-level category)
    SELECT id, name AS department, name AS level1, NULL::text AS level2, 1 AS depth
    FROM store.categories WHERE parent_id IS NULL
    UNION ALL
    SELECT c.id, t.department, t.level1, CASE WHEN t.depth = 1 THEN c.name ELSE t.level2 END, t.depth + 1
    FROM store.categories c JOIN tree t ON c.parent_id = t.id
)
SELECT t.level1,
       t.level2,
       count(DISTINCT o.id) AS orders,
       sum(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2)::numeric(14, 2)) AS revenue_usd
FROM store.orders o
JOIN store.order_items oi ON oi.order_id = o.id
JOIN store.products p ON p.id = oi.product_id
JOIN tree t ON t.id = p.category_id
JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
WHERE o.status <> 'cancelled'
  AND o.id <= @max_order_id
GROUP BY ROLLUP (t.level1, t.level2)
ORDER BY t.level1 NULLS LAST, t.level2 NULLS FIRST
