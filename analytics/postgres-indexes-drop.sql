-- Removes the indexes of postgres-indexes.sql (`make docker-indexes-drop`).
DROP INDEX IF EXISTS store.analytics_order_items_cover;
DROP INDEX IF EXISTS store.analytics_orders_by_customer_cover;
DROP INDEX IF EXISTS store.analytics_shipments_last;
