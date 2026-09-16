-- title: 08 · How DuckDB stores data and runs queries
-- engine: duckdb
--
-- Tick "EXPLAIN ANALYZE each statement" to see the plans with timings.
-- The query panel at the bottom of the page shows the time of each statement.

-- Size of every attached database (the in-memory session, the warehouse file).
PRAGMA database_size;

-- Each column is stored in segments, with its own compression per segment
-- (a columnstore index in SQL Server does the same).
SELECT column_name,
       compression,
       count(*)   AS segments,
       sum(count) AS rows
FROM pragma_storage_info('dw.fact_sales')
WHERE segment_type NOT IN ('VALIDITY')
GROUP BY ALL
ORDER BY column_name, segments DESC;

-- Zone maps: every row group stores min/max per column. fact_sales was
-- written ORDER BY date, so the date ranges of row groups do not overlap:
SELECT row_group_id, stats
FROM pragma_storage_info('dw.fact_sales')
WHERE column_name = 'order_date' AND segment_type = 'DATE'
ORDER BY row_group_id
LIMIT 5;

-- So a filter on the date skips most row groups...
SELECT count(*) AS lines, round(sum(net_usd)) AS revenue_usd
FROM dw.fact_sales
WHERE order_date BETWEEN DATE '2025-03-01' AND DATE '2025-03-07';

-- ...but customer_id is spread over every row group, so nothing is skipped.
-- Compare the two timings in the panel below (or with EXPLAIN ANALYZE).
SELECT count(*) AS lines, round(sum(net_usd)) AS revenue_usd
FROM dw.fact_sales
WHERE customer_id = 1234;

-- EXPLAIN shows the physical plan: hash joins, hash aggregates, projections.
EXPLAIN
SELECT dp.category_l1, sum(fs.net_usd)
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
GROUP BY ALL;

-- Parallelism and memory: every core, and 80% of RAM, by default.
-- Try SET threads = 1; then run the two filter queries above again.
SELECT current_setting('threads') AS threads, current_setting('memory_limit') AS memory_limit;

-- DuckDB's own catalog views (T-SQL: sys.tables, sys.columns).
SELECT schema_name, table_name, estimated_size AS rows, column_count
FROM duckdb_tables()
WHERE database_name = 'wh'
ORDER BY estimated_size DESC;
