-- Subtotals per department and a grand total with ROLLUP, walking the category tree.
-- T-SQL has no NULLS LAST, and it sorts NULLs first in ASC, so the "total" rows are pushed
-- to the end with a CASE; the level2 subtotal already comes first by default.
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
)
SELECT t.level1,
       t.level2,
       count(DISTINCT o.id) AS orders,
       sum(CAST(round((oi.unit_price * oi.quantity - oi.discount) / fx.units_per_usd, 2) AS decimal(14, 2))) AS revenue_usd
FROM store.orders o
JOIN store.order_items oi ON oi.order_id = o.id
JOIN store.products p ON p.id = oi.product_id
JOIN tree t ON t.id = p.category_id
JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = CAST(o.placed_at AS date)
WHERE o.status <> 'cancelled'
  AND o.id <= @max_order_id
GROUP BY ROLLUP (t.level1, t.level2)
ORDER BY CASE WHEN t.level1 IS NULL THEN 1 ELSE 0 END, t.level1, t.level2
