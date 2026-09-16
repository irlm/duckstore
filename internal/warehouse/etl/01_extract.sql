-- =============================================================================
-- ETL step 1: EXTRACT
--
-- Copy the OLTP tables from Postgres into the "raw" schema of the new DuckDB
-- file. Before this script runs, Go has already executed:
--
--     INSTALL postgres; LOAD postgres;
--     ATTACH 'postgres://...' AS pg (TYPE postgres, READ_ONLY);
--
-- so Postgres tables are visible as pg.store.<table>. DuckDB pulls the rows
-- with the binary COPY protocol over several connections in parallel.
--
-- SQL Server analogy: a linked server + SELECT ... INTO, but parallel.
--
-- "CREATE TABLE x AS FROM y" is DuckDB's "FROM-first" syntax. It means
-- "CREATE TABLE x AS SELECT * FROM y".
--
-- {{MAX_ORDER_ID}} is the WATERMARK: the highest order id when the ETL started.
-- Orders placed while the ETL is running wait for the next run, so the
-- warehouse never contains half of an order. The literal number (not a
-- subquery) lets DuckDB push the filter down into Postgres.
-- =============================================================================

CREATE SCHEMA raw;

-- step: raw.currencies
CREATE TABLE raw.currencies AS FROM pg.store.currencies;
-- step: raw.countries
CREATE TABLE raw.countries AS FROM pg.store.countries;
-- step: raw.fx_rates
CREATE TABLE raw.fx_rates AS FROM pg.store.fx_rates ORDER BY currency_code, rate_date;
-- step: raw.employees
CREATE TABLE raw.employees AS FROM pg.store.employees;
-- step: raw.warehouses
CREATE TABLE raw.warehouses AS FROM pg.store.warehouses;
-- step: raw.categories
CREATE TABLE raw.categories AS FROM pg.store.categories;
-- step: raw.brands
CREATE TABLE raw.brands AS FROM pg.store.brands;
-- step: raw.suppliers
CREATE TABLE raw.suppliers AS FROM pg.store.suppliers;
-- step: raw.products
CREATE TABLE raw.products AS FROM pg.store.products;
-- step: raw.product_prices
CREATE TABLE raw.product_prices AS FROM pg.store.product_prices;
-- step: raw.product_suppliers
CREATE TABLE raw.product_suppliers AS FROM pg.store.product_suppliers;
-- step: raw.inventory
CREATE TABLE raw.inventory AS FROM pg.store.inventory;
-- step: raw.promotions
CREATE TABLE raw.promotions AS FROM pg.store.promotions;
-- step: raw.promotion_products
CREATE TABLE raw.promotion_products AS FROM pg.store.promotion_products;
-- step: raw.customers
CREATE TABLE raw.customers AS FROM pg.store.customers;
-- step: raw.addresses
CREATE TABLE raw.addresses AS FROM pg.store.addresses;

-- step: raw.orders (up to the watermark)
CREATE TABLE raw.orders AS
FROM pg.store.orders
WHERE id <= {{MAX_ORDER_ID}};

-- step: raw.order_items
CREATE TABLE raw.order_items AS
FROM pg.store.order_items
WHERE order_id <= {{MAX_ORDER_ID}};

-- step: raw.payments
CREATE TABLE raw.payments AS
FROM pg.store.payments
WHERE order_id <= {{MAX_ORDER_ID}};

-- step: raw.shipments
CREATE TABLE raw.shipments AS
FROM pg.store.shipments
WHERE order_id <= {{MAX_ORDER_ID}};

-- step: raw.returns
CREATE TABLE raw.returns AS
FROM pg.store.returns
WHERE order_id <= {{MAX_ORDER_ID}};

-- step: raw.reviews
CREATE TABLE raw.reviews AS FROM pg.store.reviews;
