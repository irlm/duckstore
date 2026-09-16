-- =============================================================================
-- ETL step 2: DIMENSIONS (the "who / what / where / when" of the star schema)
--
-- A star schema has wide, denormalized dimension tables around narrow fact
-- tables. Joins that the OLTP model needs on every query (product -> brand ->
-- category tree) are done once here.
--
-- All timestamps become TIMESTAMP (no time zone, UTC) so reports do not depend
-- on the session time zone. The ETL connection runs SET TimeZone = 'UTC'.
-- =============================================================================

CREATE SCHEMA dw;

-- step: dw.dim_date
-- One row per calendar day. There is no source table: generate_series makes a
-- LIST of timestamps and unnest turns the list into rows.
-- SQL Server: a numbers table or a recursive CTE with DATEADD.
CREATE TABLE dw.dim_date AS
WITH bounds AS (
    SELECT min(placed_at)::DATE AS first_day,
           (max(placed_at) + INTERVAL 365 DAY)::DATE AS last_day   -- room for new orders
    FROM raw.orders
)
SELECT
    year(d) * 10000 + month(d) * 100 + day(d) AS date_key,   -- 20260916 (Kimball style)
    d                                         AS date,
    year(d)                                   AS year,
    quarter(d)                                AS quarter,
    month(d)                                  AS month,
    monthname(d)                              AS month_name,
    strftime(d, '%Y-%m')                      AS year_month,
    weekofyear(d)                             AS iso_week,
    isodow(d)                                 AS iso_day_of_week,  -- 1 = Monday
    dayname(d)                                AS day_name,
    isodow(d) >= 6                            AS is_weekend
FROM (
    SELECT unnest(generate_series(first_day, last_day, INTERVAL 1 DAY))::DATE AS d
    FROM bounds
);

-- step: dw.dim_category (flattened tree)
-- A RECURSIVE CTE walks the tree from the roots down. DuckDB lets the CTE
-- carry a LIST (path) that grows at each level, something you would build
-- with string concatenation in T-SQL.
CREATE TABLE dw.dim_category AS
WITH RECURSIVE tree AS (
    SELECT id, parent_id, name, 1 AS depth, [name] AS path
    FROM raw.categories
    WHERE parent_id IS NULL

    UNION ALL

    SELECT c.id, c.parent_id, c.name, t.depth + 1, list_append(t.path, c.name)
    FROM raw.categories c
    JOIN tree t ON c.parent_id = t.id
)
SELECT
    id                                AS category_id,
    name                              AS category,
    depth,
    path,                                          -- LIST(VARCHAR), e.g. ['Electronics', 'Computers', 'Laptops']
    array_to_string(path, ' > ')      AS path_text,
    path[1]                           AS level1,   -- lists are 1-based; out of range returns NULL
    path[2]                           AS level2,
    path[3]                           AS level3,
    path[4]                           AS level4,
    id NOT IN (SELECT parent_id FROM raw.categories WHERE parent_id IS NOT NULL) AS is_leaf
FROM tree;

-- step: dw.dim_product (SCD Type 2 on price)
-- Slowly Changing Dimension Type 2: one row PER VERSION of the product. The
-- price history table becomes the versions, each with valid_from/valid_to.
-- Facts join to the version that was valid when the order was placed, so a
-- 2024 sale keeps its 2024 list price even after the price changes.
-- product_key is a surrogate key (one per version), product_id the business key.
CREATE TABLE dw.dim_product AS
SELECT
    row_number() OVER (ORDER BY pp.product_id, pp.valid_from) AS product_key,
    p.id                                              AS product_id,
    p.sku,
    p.name                                            AS product_name,
    b.name                                            AS brand,
    p.category_id,
    dc.level1                                         AS category_l1,
    dc.level2                                         AS category_l2,
    dc.category                                       AS category_leaf,
    dc.path_text                                      AS category_path,
    pp.price_usd                                      AS list_price_usd,
    p.cost_usd,
    p.attributes ->> '$.color'                        AS color,           -- JSON path extraction
    (p.attributes ->> '$.warranty_months')::INTEGER   AS warranty_months,
    p.is_active,
    pp.valid_from::TIMESTAMP                          AS valid_from,
    coalesce(pp.valid_to::TIMESTAMP, TIMESTAMP '9999-12-31') AS valid_to,
    pp.valid_to IS NULL                               AS is_current
FROM raw.product_prices pp
JOIN raw.products p      ON p.id = pp.product_id
JOIN raw.brands b        ON b.id = p.brand_id
JOIN dw.dim_category dc  ON dc.category_id = p.category_id;

-- step: dw.dim_employee (management chain)
-- Each employee gets the list of ids of every manager above them. "All sales
-- under this director" then becomes list_contains(management_chain_ids, id)
-- instead of a recursive query per report.
CREATE TABLE dw.dim_employee AS
WITH RECURSIVE chain AS (
    SELECT id, manager_id, first_name || ' ' || last_name AS name, title, department, region,
           0 AS level, [id] AS chain_ids, [first_name || ' ' || last_name] AS chain_names
    FROM raw.employees
    WHERE manager_id IS NULL

    UNION ALL

    SELECT e.id, e.manager_id, e.first_name || ' ' || e.last_name, e.title, e.department, e.region,
           c.level + 1, list_append(c.chain_ids, e.id), list_append(c.chain_names, e.first_name || ' ' || e.last_name)
    FROM raw.employees e
    JOIN chain c ON e.manager_id = c.id
)
SELECT
    id                                      AS employee_id,
    manager_id,
    name,
    title,
    department,
    region,
    level,                                          -- 0 = CEO
    chain_ids                               AS management_chain_ids,
    array_to_string(chain_names, ' > ')     AS management_chain
FROM chain;

-- step: dw.dim_customer (with referral depth)
-- Customers can refer other customers, which forms chains (A -> B -> C).
-- The recursive CTE computes how deep each customer is in a chain and who
-- started it. SCD Type 1: attributes are simply overwritten on each ETL.
CREATE TABLE dw.dim_customer AS
WITH RECURSIVE referral AS (
    SELECT id AS customer_id, 0 AS referral_depth, id AS referral_root_id
    FROM raw.customers
    WHERE referred_by_customer_id IS NULL

    UNION ALL

    SELECT c.id, r.referral_depth + 1, r.referral_root_id
    FROM raw.customers c
    JOIN referral r ON c.referred_by_customer_id = r.customer_id
)
SELECT
    c.id                                   AS customer_id,
    c.first_name || ' ' || c.last_name     AS full_name,
    c.email,
    c.segment,
    c.country_code,
    co.name                                AS country,
    co.region,
    co.currency_code,
    c.created_at::TIMESTAMP                AS signup_at,
    date_trunc('month', c.created_at::TIMESTAMP)::DATE AS cohort_month,
    c.referred_by_customer_id,
    r.referral_depth,
    r.referral_root_id,
    c.account_manager_id,
    e.name                                 AS account_manager
FROM raw.customers c
JOIN raw.countries co   ON co.code = c.country_code
JOIN referral r         ON r.customer_id = c.id
LEFT JOIN dw.dim_employee e ON e.employee_id = c.account_manager_id;

-- step: dw.dim_warehouse
CREATE TABLE dw.dim_warehouse AS
SELECT w.id AS warehouse_id, w.code, w.name, w.city, w.country_code, co.name AS country, co.region,
       e.name AS manager
FROM raw.warehouses w
JOIN raw.countries co ON co.code = w.country_code
LEFT JOIN dw.dim_employee e ON e.employee_id = w.manager_employee_id;

-- step: dw.dim_promotion
CREATE TABLE dw.dim_promotion AS
SELECT p.id AS promotion_id, p.code, p.name, p.discount_pct,
       p.starts_at::TIMESTAMP AS starts_at, p.ends_at::TIMESTAMP AS ends_at,
       count(pp.product_id) AS product_count
FROM raw.promotions p
LEFT JOIN raw.promotion_products pp ON pp.promotion_id = p.id
GROUP BY ALL;   -- DuckDB: group by every non-aggregated column, no need to list them
