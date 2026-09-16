-- Runs in DuckDB with two databases attached: wh = the warehouse file (READ_ONLY) and pg = Postgres.
-- DuckDB is the data mover here: it reads its column store and writes to Postgres with the binary
-- COPY protocol, like SSIS or bcp would. Only the tables the Compare questions use are copied.

-- step: dw.dim_product
CREATE TABLE pg.dw.dim_product AS FROM wh.dw.dim_product;

-- step: dw.dim_customer
CREATE TABLE pg.dw.dim_customer AS FROM wh.dw.dim_customer;

-- step: dw.dim_employee
CREATE TABLE pg.dw.dim_employee AS FROM wh.dw.dim_employee;

-- step: dw.fact_orders
CREATE TABLE pg.dw.fact_orders AS FROM wh.dw.fact_orders;

-- step: dw.fact_sales
CREATE TABLE pg.dw.fact_sales AS FROM wh.dw.fact_sales;

-- step: dw.fact_returns
CREATE TABLE pg.dw.fact_returns AS FROM wh.dw.fact_returns;

-- step: dw.etl_info (the watermark of this copy)
CREATE TABLE pg.dw.etl_info AS FROM wh.dw.etl_info;
