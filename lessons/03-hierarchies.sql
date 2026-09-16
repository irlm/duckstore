-- title: 03 · Recursive CTEs and hierarchies
-- engine: duckdb
--
-- Three self-referencing tables: categories (tree), employees (org chart),
-- customers (referral chains). WITH RECURSIVE works like in SQL Server.
-- DuckDB can carry a LIST through the recursion, which makes paths easy.

-- Walk the category tree down from Electronics, building the path as a list.
WITH RECURSIVE tree AS (
    SELECT id, name, 1 AS depth, [name] AS path
    FROM raw.categories
    WHERE name = 'Electronics'

    UNION ALL

    SELECT c.id, c.name, t.depth + 1, list_append(t.path, c.name)
    FROM raw.categories c
    JOIN tree t ON c.parent_id = t.id
)
SELECT repeat('    ', depth - 1) || name AS category,
       depth,
       array_to_string(path, ' > ')    AS full_path
FROM tree
ORDER BY path;   -- lists sort element by element, so children follow their parent

-- Revenue of EVERY category, including all its descendants.
-- Step 1 (recursive): pair each category with all its ancestors, itself included.
-- Step 2: each sale counts once for each ancestor of its category.
WITH RECURSIVE ancestors AS (
    SELECT id AS category_id, id AS ancestor_id
    FROM raw.categories

    UNION ALL

    SELECT a.category_id, c.parent_id
    FROM ancestors a
    JOIN raw.categories c ON c.id = a.ancestor_id
    WHERE c.parent_id IS NOT NULL
)
SELECT dc.path_text                      AS category,
       dc.depth,
       round(sum(fs.net_usd) / 1e6, 2)   AS revenue_musd
FROM dw.fact_sales fs
JOIN dw.dim_product dp  USING (product_key)
JOIN ancestors a        ON a.category_id = dp.category_id
JOIN dw.dim_category dc ON dc.category_id = a.ancestor_id
GROUP BY dc.path, dc.path_text, dc.depth
ORDER BY dc.path;   -- sorting by the LIST keeps children under their parent

-- The org chart. The ETL stored each employee's chain of manager ids as a
-- LIST, so no recursion is needed here; sorting by that list gives tree order.
SELECT repeat('    ', level) || name AS employee,
       title,
       department
FROM dw.dim_employee
WHERE department IN ('Executive', 'Sales')
  AND level <= 3
ORDER BY management_chain_ids;

-- Span of control: direct reports and everyone below, using list_contains.
SELECT m.name,
       m.title,
       count(*) FILTER (WHERE e.manager_id = m.employee_id) AS direct_reports,
       count(*)                                             AS everyone_below
FROM dw.dim_employee m
JOIN dw.dim_employee e
  ON list_contains(e.management_chain_ids, m.employee_id)
 AND e.employee_id <> m.employee_id
GROUP BY ALL
ORDER BY everyone_below DESC
LIMIT 10;

-- Referral chains: who started the biggest chains, and how much do the
-- customers they brought in (directly or indirectly) spend?
WITH spend AS (
    SELECT customer_id, sum(total_usd) AS spend_usd
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
    GROUP BY ALL
)
SELECT root.full_name                          AS chain_started_by,
       count(*) - 1                            AS customers_referred,
       max(dc.referral_depth)                  AS deepest_level,
       round(sum(s.spend_usd) FILTER (WHERE dc.customer_id <> dc.referral_root_id)) AS spend_of_referred_usd
FROM dw.dim_customer dc
JOIN dw.dim_customer root ON root.customer_id = dc.referral_root_id
LEFT JOIN spend s         ON s.customer_id = dc.customer_id
GROUP BY ALL
ORDER BY customers_referred DESC
LIMIT 10;
