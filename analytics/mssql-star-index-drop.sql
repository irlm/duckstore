-- Removes the b-tree of analytics/mssql-star-index.sql, leaving the plain clustered columnstore.

SET QUOTED_IDENTIFIER ON;
GO

DROP INDEX IF EXISTS analytics_fact_orders_customer ON dw.fact_orders;
GO

SELECT 'lookup index removed' AS tuning;
GO
