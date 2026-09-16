-- Keys and indexes a DBA would give a star schema in a row store: the same kinds the store
-- tables have (primary keys, "one customer's orders by date", dates). Analytics that scan
-- most of a table will not use them; the lookup and the date-range questions can.

-- step: primary keys
ALTER TABLE dw.dim_product  ADD PRIMARY KEY (product_key);
ALTER TABLE dw.dim_customer ADD PRIMARY KEY (customer_id);
ALTER TABLE dw.dim_employee ADD PRIMARY KEY (employee_id);
ALTER TABLE dw.fact_orders  ADD PRIMARY KEY (order_id);
ALTER TABLE dw.fact_sales   ADD PRIMARY KEY (order_id, line_no);

-- step: index fact_orders (customer_id, placed_at)
CREATE INDEX fact_orders_customer_placed_idx ON dw.fact_orders (customer_id, placed_at DESC);

-- step: index fact_orders (order_date)
CREATE INDEX fact_orders_date_idx ON dw.fact_orders (order_date);

-- step: index fact_sales (order_date)
CREATE INDEX fact_sales_date_idx ON dw.fact_sales (order_date);

-- step: index fact_returns (order_id, line_no)
CREATE INDEX fact_returns_line_idx ON dw.fact_returns (order_id, line_no);

-- step: VACUUM ANALYZE (as the seed does for the store tables)
VACUUM (ANALYZE) dw.dim_product, dw.dim_customer, dw.dim_employee, dw.fact_orders, dw.fact_sales, dw.fact_returns;
