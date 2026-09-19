-- A b-tree next to the columnstore, so one table can serve reports and lookups.
-- `make docker-mssql-star-index` applies it, `make docker-mssql-star-index-drop` removes it.
--
-- The problem: a clustered columnstore has no b-tree, so "this customer's last 20 orders" has to
-- scan. On the star schema that question took 61 ms in SQL Server, against 0.1 ms in Postgres,
-- which reads it from an index. It is the one question where row storage wins outright.
--
-- SQL Server allows rowstore indexes on a columnstore table, which is the usual answer: the
-- columnstore answers the reports, the b-tree answers the lookups, in the same table. Measured
-- here: 61 ms -> 1 ms, and no other question changed.
--
-- The price: space, and slower writes into the fact table.

SET QUOTED_IDENTIFIER ON;
SET STATISTICS TIME ON;
GO

CREATE NONCLUSTERED INDEX analytics_fact_orders_customer
    ON dw.fact_orders (customer_id, placed_at DESC)
    INCLUDE (order_id, status, line_count, total_usd);
GO

UPDATE STATISTICS dw.fact_orders WITH FULLSCAN;
GO

SELECT 'lookup index added' AS tuning;
GO
