-- Undoes analytics/mssql-star-tuning.sql (`make docker-mssql-star-tuning-drop`): the rowstore
-- index goes, and both facts go back to a plain, unordered clustered columnstore index —
-- the state etl/mssql/02_star_indexes.sql leaves after a load.

SET QUOTED_IDENTIFIER ON;
GO

DROP INDEX IF EXISTS analytics_fact_orders_customer ON dw.fact_orders;
GO

CREATE CLUSTERED COLUMNSTORE INDEX cci_fact_sales
    ON dw.fact_sales
    WITH (DROP_EXISTING = ON, MAXDOP = 4);
GO

CREATE CLUSTERED COLUMNSTORE INDEX cci_fact_orders
    ON dw.fact_orders
    WITH (DROP_EXISTING = ON, MAXDOP = 4);
GO

UPDATE STATISTICS dw.fact_sales WITH FULLSCAN;
GO
UPDATE STATISTICS dw.fact_orders WITH FULLSCAN;
GO

SELECT 'star tuning removed' AS tuning;
GO
