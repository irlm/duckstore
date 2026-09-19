-- Removes everything analytics/mssql-tuning.sql added (`make docker-mssql-tuning-drop`),
-- so the questions can be measured again on the plain tables.
-- The index has to go first: a computed column cannot be dropped while an index uses it.

SET QUOTED_IDENTIFIER ON;
GO

DROP INDEX IF EXISTS analytics_orders_placed_date ON store.orders;
GO

ALTER TABLE store.orders DROP COLUMN IF EXISTS placed_date;
GO

UPDATE STATISTICS store.orders WITH FULLSCAN;
GO

SELECT 'placed_date removed' AS tuning;
GO
