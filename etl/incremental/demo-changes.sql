-- Changes a store makes during a day, to try the incremental ETL. Each one tests a rule of incremental.sql:
--   docker compose exec -T postgres psql -U store -d store -f - < etl/incremental/demo-changes.sql
-- (New orders: run the load test, or place orders in the store.)
\timing on
BEGIN;

-- 1,000 old paid orders ship ("old": not among the newest 20,000, so below the warehouse watermark): orders.updated_at changes, the shipments get shipped_at.
WITH picked AS (SELECT id FROM store.orders WHERE status = 'paid' AND id <= (SELECT max(id) - 20000 FROM store.orders) ORDER BY id LIMIT 1000),
     ship AS (UPDATE store.shipments SET status = 'in_transit', shipped_at = now()
              WHERE order_id IN (SELECT id FROM picked) AND status = 'preparing')
UPDATE store.orders SET status = 'shipped', updated_at = now() WHERE id IN (SELECT id FROM picked);

-- 500 old shipped orders are delivered.
WITH picked AS (SELECT id FROM store.orders WHERE status = 'shipped' AND id <= (SELECT max(id) - 20000 FROM store.orders) ORDER BY id DESC LIMIT 500),
     arrive AS (UPDATE store.shipments SET status = 'delivered', delivered_at = now()
                WHERE order_id IN (SELECT id FROM picked) AND status = 'in_transit')
UPDATE store.orders SET status = 'delivered', updated_at = now() WHERE id IN (SELECT id FROM picked);

-- 200 old paid orders are cancelled: their lines must leave fact_sales.
UPDATE store.orders SET status = 'cancelled', updated_at = now()
WHERE id IN (SELECT id FROM store.orders WHERE status = 'paid' AND id <= (SELECT max(id) - 20000 FROM store.orders) ORDER BY id DESC LIMIT 200);

-- 300 returns on old delivered orders. The order row does not change: found by returns.id.
INSERT INTO store.returns (order_id, line_no, quantity, reason, status, refund_amount, requested_at)
SELECT oi.order_id, oi.line_no, 1, 'changed_mind', 'requested', oi.unit_price, now()
FROM store.order_items oi
JOIN store.orders o ON o.id = oi.order_id
WHERE o.status = 'delivered' AND o.id BETWEEN 11000000 AND 11002000 AND oi.line_no = 1
  AND NOT EXISTS (SELECT 1 FROM store.returns r WHERE r.order_id = oi.order_id AND r.line_no = oi.line_no)
LIMIT 300;

-- 100 products get a 10% higher price: a new version in product_prices (SCD Type 2 in dim_product).
CREATE TEMP TABLE repriced AS SELECT id, round(price_usd * 1.10, 2) AS new_price FROM store.products WHERE is_active ORDER BY id LIMIT 100;
UPDATE store.product_prices pp SET valid_to = now() FROM repriced r WHERE pp.product_id = r.id AND pp.valid_to IS NULL;
INSERT INTO store.product_prices (product_id, valid_from, price_usd) SELECT id, now(), new_price FROM repriced;
UPDATE store.products p SET price_usd = r.new_price FROM repriced r WHERE p.id = r.id;

-- 20 products are renamed: overwritten on every version (SCD Type 1 columns).
UPDATE store.products SET name = name || ' (renamed)' WHERE id IN (SELECT id FROM store.products ORDER BY id DESC LIMIT 20);

COMMIT;
