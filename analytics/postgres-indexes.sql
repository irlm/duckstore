\timing on
-- Indexes a DBA could add to the Postgres store tables for the Compare questions.
-- Not part of the schema: `make docker-indexes` creates them, `make docker-indexes-drop` removes them,
-- so the Compare page can measure Postgres · store tables with and without them.
--
-- All are covering indexes (INCLUDE): the query finds every column it needs in the index, so Postgres
-- can do an index-only scan and skip the table. SQL Server: CREATE INDEX ... INCLUDE (...), the same idea.
-- The price: disk space, a longer VACUUM, and slower INSERT/UPDATE on orders and order_items, because
-- every write must also update these indexes. The Compare docs show the numbers.

SET maintenance_work_mem = '1GB';
SET max_parallel_maintenance_workers = 4;

-- Revenue questions: the plans loop over 12M orders and look up their lines in order_items_pkey, reading
-- the table for every line. With the other columns in the index, each lookup is index-only.
CREATE INDEX IF NOT EXISTS analytics_order_items_cover
    ON store.order_items (order_id) INCLUDE (line_no, product_id, quantity, unit_price, discount);

-- Per-customer questions (cohort retention, RFM segments, lifetime value): rows can come already
-- grouped by customer and in date order, instead of hashing 12M orders into 1.5M groups.
CREATE INDEX IF NOT EXISTS analytics_orders_by_customer_cover
    ON store.orders (customer_id, placed_at) INCLUDE (id, status, currency_code, total);

-- Delivery percentiles: DISTINCT ON (order_id) ... ORDER BY order_id, delivered_at DESC NULLS FIRST, ...
-- The plan reads shipments_order_idx and then sorts each order's parcels; this index is already in
-- that order and has every column, so there is no sort and no table access.
CREATE INDEX IF NOT EXISTS analytics_shipments_last
    ON store.shipments (order_id, delivered_at DESC NULLS FIRST, shipped_at DESC NULLS FIRST) INCLUDE (carrier);

-- Index-only scans need the visibility map (which pages have only rows every transaction can see),
-- and the planner needs fresh statistics.
VACUUM (ANALYZE) store.orders, store.order_items, store.shipments;
