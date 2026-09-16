-- title: 01 · Friendly SQL: DuckDB shortcuts
-- engine: duckdb
--
-- DuckDB speaks standard SQL, plus shortcuts that remove boilerplate.
-- Run the script: every statement shows its own result below the editor.

-- FROM-first: SELECT * is optional.  (T-SQL: SELECT TOP 5 * FROM ...)
FROM dw.fact_orders LIMIT 5;

-- DESCRIBE shows columns and types.  (T-SQL: sp_help 'table')
DESCRIBE dw.fact_sales;

-- SUMMARIZE profiles every column: min, max, distinct count, nulls, quartiles.
-- There is no one-line equivalent in SQL Server.
SUMMARIZE dw.dim_customer;

-- GROUP BY ALL / ORDER BY ALL: no need to repeat the columns.
SELECT year(order_date) AS year, status, count(*) AS orders
FROM dw.fact_orders
GROUP BY ALL
ORDER BY ALL;

-- SELECT * EXCLUDE / REPLACE: all columns except some, or with one rewritten.
SELECT * EXCLUDE (fx_rate, currency_code) REPLACE (round(total_usd) AS total_usd)
FROM dw.fact_orders
LIMIT 5;

-- COLUMNS(): apply one expression to many columns, chosen by a regular expression.
SELECT max(COLUMNS('.*_usd'))
FROM dw.fact_orders;

-- Column aliases can be used in WHERE, GROUP BY and later in the same SELECT.
SELECT total_usd - tax_usd AS net_usd,
       net_usd * 0.1        AS commission_usd
FROM dw.fact_orders
WHERE net_usd > 5000
LIMIT 5;

-- String functions with dot syntax.
SELECT product_name, product_name.upper().split(' ')[1] AS brand_upper
FROM dw.dim_product
LIMIT 5;
