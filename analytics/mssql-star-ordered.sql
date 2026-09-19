-- An experiment that did not pay off, kept because the result is worth knowing.
-- `make docker-mssql-star-ordered` applies it, `make docker-mssql-star-ordered-drop` undoes it.
--
-- The idea: a columnstore skips row groups only when the values it needs sit together, so sorting
-- the facts by order_date while rebuilding should let whole row groups be skipped for every
-- question about a date range (segment elimination).
--
-- The measurement said otherwise. On this data the rows were already loaded in order_id order,
-- which follows the date closely, so there was nothing left to gain — and the rebuild produced
-- worse segments for everything else: delivery percentiles 12.4 s -> 26.2 s, cohort retention
-- 1.2 s -> 2.0 s, the category rollup 84 s -> 100 s, with no question measurably faster.
-- See docs/results/bench-star-scale5-laptop.md.
--
-- The lesson: ORDER is worth it when the load order and the filter column disagree. Measure before
-- paying for a rebuild that takes minutes.

SET QUOTED_IDENTIFIER ON;
SET STATISTICS TIME ON;
GO

-- Order the fact rows by date, so a date filter could skip row groups.
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

SELECT 'ordered columnstore applied' AS tuning;
GO
