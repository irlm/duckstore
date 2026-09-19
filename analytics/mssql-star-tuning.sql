-- What a SQL Server DBA adds to the star schema after reading the first results.
-- `make docker-mssql-star-tuning` applies it, `make docker-mssql-star-tuning-drop` removes it,
-- so the same questions can be measured with and without.
--
-- The store-table tuning (analytics/mssql-tuning.sql) cannot help here: the star questions never
-- read store.orders. The star schema has two different problems.
--
-- 1. A clustered columnstore has no b-tree, so "this customer's last 20 orders" scans: 61 ms,
--    where Postgres answers the same question from an index in 0.1 ms. SQL Server allows a
--    rowstore index on a columnstore table, which is the usual answer — the columnstore serves
--    the reports, the b-tree serves the lookups, in one table.
--
-- 2. Columnstore skips row groups only when the values it needs sit together. The fact table was
--    loaded in order_id order, so a filter on order_date touches nearly every row group. An
--    ORDERed columnstore sorts the data by that column while rebuilding, which lets whole row
--    groups be skipped (segment elimination) for every question that asks about a date range.
--
-- The price: the rebuild takes minutes and the extra index costs space and slows writes.

SET QUOTED_IDENTIFIER ON;
SET STATISTICS TIME ON;
GO

-- 1. A b-tree for the lookup, next to the columnstore.
CREATE NONCLUSTERED INDEX analytics_fact_orders_customer
    ON dw.fact_orders (customer_id, placed_at DESC)
    INCLUDE (order_id, status, line_count, total_usd);
GO

-- 2. Order the fact rows by date, so a date filter can skip row groups.
CREATE CLUSTERED COLUMNSTORE INDEX cci_fact_sales
    ON dw.fact_sales
    ORDER (order_date)
    WITH (DROP_EXISTING = ON, MAXDOP = 4);
GO

CREATE CLUSTERED COLUMNSTORE INDEX cci_fact_orders
    ON dw.fact_orders
    ORDER (order_date)
    WITH (DROP_EXISTING = ON, MAXDOP = 4);
GO

UPDATE STATISTICS dw.fact_sales WITH FULLSCAN;
GO
UPDATE STATISTICS dw.fact_orders WITH FULLSCAN;
GO

SELECT 'star tuning applied' AS tuning;
GO
