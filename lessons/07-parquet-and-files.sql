-- title: 07 · Parquet and CSV: query files without loading them
-- engine: duckdb
--
-- The ETL exported the warehouse to data/parquet (paths are relative to the
-- folder where the server was started: the project folder).
-- Parquet is a columnar FILE format: DuckDB, Spark, pandas, Power BI and
-- Fabric can all read it. There is no import step.

-- A glob reads every file of the partitioned folder.
SELECT count(*)            AS order_lines,
       round(sum(net_usd)) AS revenue_usd
FROM read_parquet('data/parquet/fact_sales/*/*/*.parquet', hive_partitioning = true);

-- Hive partitioning: year and month come from folder names (year=2025/month=11).
-- A filter on them skips whole folders; only matching files are opened.
SELECT year, month, count(*) AS order_lines
FROM read_parquet('data/parquet/fact_sales/*/*/*.parquet', hive_partitioning = true)
WHERE year = 2025 AND month IN (11, 12)
GROUP BY ALL
ORDER BY ALL;

-- filename = true adds the file each row came from.
SELECT filename, count(*) AS order_lines
FROM read_parquet('data/parquet/fact_sales/*/*/*.parquet', hive_partitioning = true, filename = true)
WHERE year = 2026 AND month <= 3
GROUP BY ALL
ORDER BY filename;

-- Parquet keeps min/max statistics and compression info per row group and column.
SELECT path_in_schema AS column_name, compression, stats_min, stats_max, total_compressed_size
FROM parquet_metadata('data/parquet/fact_orders.parquet')
WHERE row_group_id = 0
ORDER BY column_id;

-- A file name can be used like a table name, and joined like one.
SELECT c.country,
       round(sum(o.total_usd)) AS revenue_usd
FROM 'data/parquet/fact_orders.parquet' o
JOIN 'data/parquet/dim_customer.parquet' c USING (customer_id)
GROUP BY ALL
ORDER BY revenue_usd DESC
LIMIT 5;

-- DESCRIBE works on files too.
DESCRIBE SELECT * FROM 'data/parquet/dim_product.parquet';

-- Write a CSV and read it back. (T-SQL: bcp or BULK INSERT)
COPY (SELECT * FROM dw.dim_warehouse ORDER BY warehouse_id) TO 'data/warehouses.csv' (HEADER);
SELECT * FROM read_csv('data/warehouses.csv');

-- read_csv detected the types by sampling the file:
DESCRIBE SELECT * FROM read_csv('data/warehouses.csv');
