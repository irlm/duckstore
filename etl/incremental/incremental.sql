-- =============================================================================
-- Incremental ETL: bring an existing warehouse up to date with what changed in
-- Postgres since the last run, instead of rebuilding everything.
--
-- The C# runner (WarehouseBuilder, mode Incremental):
--   1. copies the warehouse file with a reflink (copy-on-write: 2.6 GB in ~50 ms)
--   2. reads the last run's watermark and time from dw.etl_info
--   3. runs this file. A line "-- @full: <name>" runs steps of the full ETL at that point, with
--      CREATE OR REPLACE TABLE: the small raw tables and dimensions (cheap to copy again), the fact
--      SQL on the changed orders only, and reviews, inventory and marts
--   4. swaps the file in, like a full ETL
--
-- Placeholders, filled in by the runner just before each statement:
--   {{PREVIOUS_MAX_ORDER_ID}}  the last run's watermark
--   {{MAX_ORDER_ID}}           this run's watermark
--   {{CHANGES_SINCE}}          the last run's extraction time (UTC) minus 5 minutes:
--                              a change committed while the last run was reading is not lost,
--                              and handling an order twice gives the same result
--   {{MAX_RETURN_ID}}          the highest return id already loaded
--   {{OLD_CHANGED_IDS}}        ids of changed orders at or below the old watermark, as '{1,2,3}'
--
-- postgres_query('pg', '...') sends the SQL text to Postgres as it is, so it uses Postgres
-- indexes and Postgres syntax. It returns the rows to DuckDB.
-- =============================================================================

-- step: find new and changed orders
CREATE TEMP TABLE changed_orders AS
SELECT id FROM postgres_query('pg', $$
    SELECT id FROM store.orders
    WHERE id > {{PREVIOUS_MAX_ORDER_ID}} AND id <= {{MAX_ORDER_ID}}
    UNION
    -- status changes (shipped, delivered, cancelled) set updated_at
    SELECT id FROM store.orders
    WHERE id <= {{PREVIOUS_MAX_ORDER_ID}} AND updated_at > '{{CHANGES_SINCE}}'::timestamp AT TIME ZONE 'UTC'
    UNION
    -- a new return changes its order's facts even if the order row did not change
    SELECT order_id FROM store.returns
    WHERE id > {{MAX_RETURN_ID}} AND order_id <= {{MAX_ORDER_ID}}
$$);

-- step: delta: orders
CREATE TABLE delta.raw.orders AS
SELECT * FROM postgres_query('pg', $$
    SELECT * FROM store.orders
    WHERE (id > {{PREVIOUS_MAX_ORDER_ID}} AND id <= {{MAX_ORDER_ID}}) OR id = ANY ('{{OLD_CHANGED_IDS}}'::bigint[])
$$);

-- step: delta: order_items
CREATE TABLE delta.raw.order_items AS
SELECT * FROM postgres_query('pg', $$
    SELECT * FROM store.order_items
    WHERE (order_id > {{PREVIOUS_MAX_ORDER_ID}} AND order_id <= {{MAX_ORDER_ID}}) OR order_id = ANY ('{{OLD_CHANGED_IDS}}'::bigint[])
$$);

-- step: delta: payments
CREATE TABLE delta.raw.payments AS
SELECT * FROM postgres_query('pg', $$
    SELECT * FROM store.payments
    WHERE (order_id > {{PREVIOUS_MAX_ORDER_ID}} AND order_id <= {{MAX_ORDER_ID}}) OR order_id = ANY ('{{OLD_CHANGED_IDS}}'::bigint[])
$$);

-- step: delta: shipments
CREATE TABLE delta.raw.shipments AS
SELECT * FROM postgres_query('pg', $$
    SELECT * FROM store.shipments
    WHERE (order_id > {{PREVIOUS_MAX_ORDER_ID}} AND order_id <= {{MAX_ORDER_ID}}) OR order_id = ANY ('{{OLD_CHANGED_IDS}}'::bigint[])
$$);

-- step: delta: returns
CREATE TABLE delta.raw.returns AS
SELECT * FROM postgres_query('pg', $$
    SELECT * FROM store.returns
    WHERE (order_id > {{PREVIOUS_MAX_ORDER_ID}} AND order_id <= {{MAX_ORDER_ID}}) OR order_id = ANY ('{{OLD_CHANGED_IDS}}'::bigint[])
$$);

-- @full: reference tables
-- All raw tables except the five order tables, copied again: 18 tables, ~5M rows, a few seconds.

-- step: raw order tables: replace the changed orders
-- Delete + insert instead of UPDATE: a changed order can gain or lose lines, shipments, returns.
-- New rows are appended in date order, so the zone maps of old blocks stay tight.
DELETE FROM raw.orders      WHERE id       IN (SELECT id FROM changed_orders);
DELETE FROM raw.order_items WHERE order_id IN (SELECT id FROM changed_orders);
DELETE FROM raw.payments    WHERE order_id IN (SELECT id FROM changed_orders);
DELETE FROM raw.shipments   WHERE order_id IN (SELECT id FROM changed_orders);
DELETE FROM raw.returns     WHERE order_id IN (SELECT id FROM changed_orders);
INSERT INTO raw.orders      FROM delta.raw.orders      ORDER BY placed_at;
INSERT INTO raw.order_items FROM delta.raw.order_items ORDER BY order_id, line_no;
INSERT INTO raw.payments    FROM delta.raw.payments    ORDER BY order_id;
INSERT INTO raw.shipments   FROM delta.raw.shipments   ORDER BY order_id;
INSERT INTO raw.returns     FROM delta.raw.returns     ORDER BY order_id;

-- @full: dimensions
-- Every dimension except dim_product, rebuilt from the refreshed raw tables (customers change too).

-- step: dw.dim_product: new price versions (SCD Type 2 with stable keys)
-- The full ETL numbers every version with row_number(), which is fine when the facts are rebuilt too.
-- Here old sales keep their product_key, so existing keys must never change: a version that ended
-- gets its valid_to, and a new version gets a key after the highest existing one.
UPDATE dw.dim_product d
SET valid_to = pp.valid_to::TIMESTAMP, is_current = false
FROM raw.product_prices pp
WHERE pp.product_id = d.product_id
  AND pp.valid_from::TIMESTAMP = d.valid_from
  AND pp.valid_to IS NOT NULL
  AND d.is_current;

INSERT INTO dw.dim_product
SELECT
    (SELECT max(product_key) FROM dw.dim_product)
        + row_number() OVER (ORDER BY pp.product_id, pp.valid_from) AS product_key,
    p.id, p.sku, p.name, b.name, p.category_id, dc.level1, dc.level2, dc.category, dc.path_text,
    pp.price_usd, p.cost_usd, (p.attributes ->> '$.color'), (p.attributes ->> '$.warranty_months')::INTEGER,
    p.is_active, pp.valid_from::TIMESTAMP, coalesce(pp.valid_to::TIMESTAMP, TIMESTAMP '9999-12-31'), pp.valid_to IS NULL
FROM raw.product_prices pp
JOIN raw.products p      ON p.id = pp.product_id
JOIN raw.brands b        ON b.id = p.brand_id
JOIN dw.dim_category dc  ON dc.category_id = p.category_id
WHERE NOT EXISTS (SELECT 1 FROM dw.dim_product d
                  WHERE d.product_id = pp.product_id AND d.valid_from = pp.valid_from::TIMESTAMP);

-- Attributes that are not versioned (name, brand, category, cost, active) are overwritten on every
-- version, as the full ETL does (SCD Type 1 for these columns).
UPDATE dw.dim_product d
SET sku = p.sku, product_name = p.name, brand = b.name, category_id = p.category_id,
    category_l1 = dc.level1, category_l2 = dc.level2, category_leaf = dc.category, category_path = dc.path_text,
    cost_usd = p.cost_usd, color = (p.attributes ->> '$.color'),
    warranty_months = (p.attributes ->> '$.warranty_months')::INTEGER, is_active = p.is_active
FROM raw.products p
JOIN raw.brands b       ON b.id = p.brand_id
JOIN dw.dim_category dc ON dc.category_id = p.category_id
WHERE d.product_id = p.id
  -- only rows that really changed: an UPDATE rewrites the row even when the values are the same.
  -- The parentheses around ->> matter: in DuckDB ->> binds more loosely than OR, so without them the
  -- whole OR chain becomes the left side of ->> and the condition is never true.
  AND (d.sku IS DISTINCT FROM p.sku OR d.product_name IS DISTINCT FROM p.name OR d.brand IS DISTINCT FROM b.name
       OR d.category_id IS DISTINCT FROM p.category_id OR d.category_path IS DISTINCT FROM dc.path_text
       OR d.cost_usd IS DISTINCT FROM p.cost_usd OR d.color IS DISTINCT FROM (p.attributes ->> '$.color')
       OR d.warranty_months IS DISTINCT FROM (p.attributes ->> '$.warranty_months')::INTEGER
       OR d.is_active IS DISTINCT FROM p.is_active);

-- @full: facts of the changed orders
-- The full ETL's fact_orders, fact_sales and fact_returns statements run in the in-memory database
-- "delta", where raw.orders, raw.order_items... hold only the changed orders, and raw.fx_rates and
-- dw.dim_product are views of the warehouse. The results land in delta.dw.*.

-- step: facts: replace the changed orders
-- delta.dw.fact_orders, fact_sales and fact_returns were built by the full ETL's own SQL, run on the
-- delta rows (see WarehouseBuilder). Their customer_order_number only counts the delta: fixed below.
DELETE FROM dw.fact_returns WHERE order_id IN (SELECT id FROM changed_orders);
DELETE FROM dw.fact_sales   WHERE order_id IN (SELECT id FROM changed_orders);
DELETE FROM dw.fact_orders  WHERE order_id IN (SELECT id FROM changed_orders);
INSERT INTO dw.fact_orders  FROM delta.dw.fact_orders  ORDER BY placed_at;
INSERT INTO dw.fact_sales   FROM delta.dw.fact_sales   ORDER BY placed_at, order_id, line_no;
INSERT INTO dw.fact_returns FROM delta.dw.fact_returns ORDER BY return_date;

-- step: dw.fact_orders: renumber the orders of affected customers
-- customer_order_number (1 = first order) needs the customer's whole history, not only the delta.
UPDATE dw.fact_orders f
SET customer_order_number = n.customer_order_number
FROM (
    SELECT order_id,
           row_number() OVER (PARTITION BY customer_id ORDER BY placed_at, order_id) AS customer_order_number
    FROM dw.fact_orders
    WHERE customer_id IN (SELECT customer_id FROM delta.raw.orders)
) n
WHERE f.order_id = n.order_id
  AND f.customer_order_number <> n.customer_order_number;

-- @full: reviews, inventory and marts
-- fact_reviews and fact_inventory are rebuilt (cheap); the marts are recomputed from the updated facts.
