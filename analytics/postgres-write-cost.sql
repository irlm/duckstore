-- What indexes cost on writes: copy 100,000 orders with their lines and shipments, then ROLLBACK.
-- Run it before and after `make docker-indexes` and compare the INSERT times:
--   docker compose exec -T postgres psql -U store -d store -f - < analytics/postgres-write-cost.sql
-- Every index on a table must be updated for every inserted row, like in SQL Server.
\timing on
BEGIN;
INSERT INTO store.orders (id, customer_id, shipping_address_id, promotion_id, status, currency_code,
                          subtotal, discount, shipping_fee, tax, total, placed_at, updated_at)
SELECT id + 1000000000, customer_id, shipping_address_id, promotion_id, status, currency_code,
       subtotal, discount, shipping_fee, tax, total, placed_at, updated_at
FROM store.orders WHERE id <= 100000;
INSERT INTO store.order_items (order_id, line_no, product_id, warehouse_id, quantity, unit_price, discount)
SELECT order_id + 1000000000, line_no, product_id, warehouse_id, quantity, unit_price, discount
FROM store.order_items WHERE order_id <= 100000;
INSERT INTO store.shipments (order_id, warehouse_id, carrier, tracking_number, status, shipped_at, delivered_at)
SELECT order_id + 1000000000, warehouse_id, carrier, tracking_number || '-copy', status, shipped_at, delivered_at
FROM store.shipments WHERE order_id <= 100000;
ROLLBACK;
