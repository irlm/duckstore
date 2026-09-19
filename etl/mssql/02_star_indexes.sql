-- The star schema in SQL Server, stored the way a SQL Server team would store it:
-- a CLUSTERED COLUMNSTORE INDEX on the facts, rowstore dimensions.
--
-- This is the comparison that matters for DuckDB: columnstore keeps each column apart
-- and compressed, and lets the engine run operators in batch mode instead of row by row.
-- etl/mssql/03_star_rowstore.sql is the same schema without it, to measure what it is worth.

CREATE UNIQUE CLUSTERED INDEX pk_dim_category ON dw.dim_category (category_id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_dim_customer ON dw.dim_customer (customer_id);
GO
CREATE UNIQUE CLUSTERED INDEX pk_dim_employee ON dw.dim_employee (employee_id);
GO
-- SCD2: one row per product version, so the key is the surrogate product_key.
CREATE UNIQUE CLUSTERED INDEX pk_dim_product  ON dw.dim_product (product_key);
GO
CREATE NONCLUSTERED INDEX dim_product_product_idx ON dw.dim_product (product_id);
GO

CREATE CLUSTERED COLUMNSTORE INDEX cci_fact_orders  ON dw.fact_orders;
GO
CREATE CLUSTERED COLUMNSTORE INDEX cci_fact_sales   ON dw.fact_sales;
GO
CREATE CLUSTERED COLUMNSTORE INDEX cci_fact_returns ON dw.fact_returns;
GO

EXEC sp_updatestats;
GO
