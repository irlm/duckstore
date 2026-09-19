-- Keys and indexes for the store tables in SQL Server, mirroring what Postgres has
-- (internal/pg/schema/02_constraints.sql). Neither engine gets a head start: the same
-- key on the same columns, and no extra "analytics" index on either side.
--
-- These are UNIQUE CLUSTERED INDEXes rather than PRIMARY KEYs because the loader copies
-- the warehouse's columns as nullable; for the query planner a unique clustered index is
-- the same thing as a clustered primary key.
--
-- Batches are separated by a line with only GO, as sqlcmd does.

CREATE UNIQUE CLUSTERED INDEX pk_brands          ON store.brands (id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_categories      ON store.categories (id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_products        ON store.products (id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_customers       ON store.customers (id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_employees       ON store.employees (id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_fx_rates        ON store.fx_rates (currency_code, rate_date);
GO
CREATE UNIQUE CLUSTERED INDEX pk_fx_rates_daily  ON store.fx_rates_daily (currency_code, day);
GO
CREATE UNIQUE CLUSTERED INDEX pk_orders          ON store.orders (id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_order_items     ON store.order_items (order_id, line_no);
GO
CREATE UNIQUE CLUSTERED INDEX pk_shipments       ON store.shipments (id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_returns         ON store.returns (id);
GO

-- Secondary indexes: one for one with the Postgres set.
CREATE NONCLUSTERED INDEX categories_parent_idx   ON store.categories (parent_id);
GO
CREATE NONCLUSTERED INDEX products_category_idx   ON store.products (category_id);
GO
CREATE NONCLUSTERED INDEX products_brand_idx      ON store.products (brand_id);
GO
CREATE NONCLUSTERED INDEX customers_referred_idx  ON store.customers (referred_by_customer_id);
GO
CREATE NONCLUSTERED INDEX customers_acct_mgr_idx  ON store.customers (account_manager_id);
GO
CREATE NONCLUSTERED INDEX employees_manager_idx   ON store.employees (manager_id);
GO
-- The "my orders" page: newest first for one customer.
CREATE NONCLUSTERED INDEX orders_customer_placed_idx ON store.orders (customer_id, placed_at DESC);
GO
CREATE NONCLUSTERED INDEX orders_placed_idx       ON store.orders (placed_at);
GO
CREATE NONCLUSTERED INDEX order_items_product_idx ON store.order_items (product_id);
GO
CREATE NONCLUSTERED INDEX shipments_order_idx     ON store.shipments (order_id);
GO
CREATE NONCLUSTERED INDEX returns_order_line_idx  ON store.returns (order_id, line_no);
GO

-- Statistics for the whole schema, the way the ETL runs ANALYZE on Postgres.
EXEC sp_updatestats;
GO
