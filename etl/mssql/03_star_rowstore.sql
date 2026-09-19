-- The same star schema as 02_star_indexes.sql, but the facts stay rowstore, with the
-- indexes the Postgres copy has (etl/postgres-star/3_after.postgres.sql).
--
-- Load with `mssql-load --no-columnstore` to answer one question: how much of SQL Server's
-- speed on the star schema comes from columnstore, and how much from the model itself?

CREATE UNIQUE CLUSTERED INDEX pk_dim_category ON dw.dim_category (category_id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_dim_customer ON dw.dim_customer (customer_id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_dim_employee ON dw.dim_employee (employee_id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_dim_product  ON dw.dim_product (product_key);
GO
CREATE NONCLUSTERED INDEX dim_product_product_idx ON dw.dim_product (product_id);
GO

CREATE UNIQUE CLUSTERED INDEX pk_fact_orders  ON dw.fact_orders (order_id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_fact_sales   ON dw.fact_sales (order_id, line_no);
GO
CREATE UNIQUE CLUSTERED INDEX pk_fact_returns ON dw.fact_returns (return_id);
GO

CREATE NONCLUSTERED INDEX fact_orders_customer_idx ON dw.fact_orders (customer_id, placed_at);
GO
CREATE NONCLUSTERED INDEX fact_orders_date_idx     ON dw.fact_orders (order_date);
GO
CREATE NONCLUSTERED INDEX fact_sales_date_idx      ON dw.fact_sales (order_date);
GO
CREATE NONCLUSTERED INDEX fact_returns_line_idx    ON dw.fact_returns (order_id, line_no);
GO

EXEC sp_updatestats;
GO
