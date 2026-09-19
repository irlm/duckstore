-- What a SQL Server DBA adds after reading the plan of the Compare questions.
-- Not part of the schema: `make docker-mssql-tuning` applies it, `make docker-mssql-tuning-drop`
-- removes it, so the same questions can be measured with and without the tuning.
--
-- The problem it fixes. Seven of the fifteen questions join the daily exchange-rate table on
-- the date of the order:
--
--   JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code
--                               AND fx.day = CAST(o.placed_at AS date)
--
-- SQL Server has no statistics for the expression CAST(placed_at AS date), estimates the join
-- as tiny, and chooses a nested-loop join: one index seek per row, 26.5 million times. Revenue
-- per month took 84.8 s, and UPDATE STATISTICS ... WITH FULLSCAN made it worse (101.7 s),
-- because the estimate of an expression is the part that is missing, not the freshness of the
-- table statistics. The same query with OPTION (HASH JOIN) took 5.6 s.
--
-- The fix below is the one to ship: a PERSISTED computed column holds the date, so it is a real
-- column with real statistics, and the optimizer matches the expression in the query to it
-- automatically — the SQL of the questions does not change. It is the same lesson Postgres
-- taught here earlier: the daily rate calendar had to become a real table before the planner
-- could estimate it (see internal/pg/schema/03_fx_rates_daily.sql).
--
-- The price, as with any index: space, and every INSERT or UPDATE of an order must maintain
-- the column and the index.

-- sqlcmd connects with QUOTED_IDENTIFIER OFF, and SQL Server refuses to create an index on a
-- computed column unless it is ON: the setting is stored with the index and must match when the
-- column is maintained.
SET QUOTED_IDENTIFIER ON;
SET STATISTICS TIME ON;
GO

ALTER TABLE store.orders ADD placed_date AS CAST(placed_at AS date) PERSISTED;
GO

-- The index makes the date a seek key as well, and carries the columns the questions read, so
-- the join can take them from the index instead of the table.
CREATE NONCLUSTERED INDEX analytics_orders_placed_date
    ON store.orders (placed_date)
    INCLUDE (id, customer_id, currency_code, status, total);
GO

-- Statistics on the new column, at full precision: this is what the join estimate reads.
UPDATE STATISTICS store.orders WITH FULLSCAN;
GO

SELECT 'placed_date added and indexed' AS tuning;
GO
