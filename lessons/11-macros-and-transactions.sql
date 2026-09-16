-- title: 11 · Macros, temp tables and transactions
-- engine: duckdb
--
-- The warehouse is attached READ_ONLY, but each console run has its own
-- session where TEMP objects can be created. They disappear afterwards.

-- A scalar macro (T-SQL: a scalar function, but expanded inline like a view,
-- so it costs nothing at run time).
CREATE OR REPLACE TEMP MACRO to_usd(amount, rate) AS round(amount / rate, 2);

SELECT order_id, currency_code, total_local, to_usd(total_local, fx_rate) AS total_usd
FROM dw.fact_orders
ORDER BY order_id
LIMIT 5;

-- A table macro (T-SQL: an inline table-valued function) with parameters.
CREATE OR REPLACE TEMP MACRO top_products(department, n) AS TABLE
    SELECT dp.product_name, round(sum(fs.net_usd)) AS revenue_usd
    FROM dw.fact_sales fs
    JOIN dw.dim_product dp USING (product_key)
    WHERE dp.category_l1 = department
    GROUP BY ALL
    ORDER BY revenue_usd DESC
    LIMIT n;

FROM top_products('Toys', 5);

-- A TEMP table lives in memory for this session only.
CREATE TEMP TABLE vip_spend AS
SELECT fo.customer_id, sum(fo.total_usd) AS spend_usd
FROM dw.fact_orders fo
JOIN dw.dim_customer dc USING (customer_id)
WHERE dc.segment = 'vip' AND fo.status <> 'cancelled'
GROUP BY ALL;

SELECT count(*) AS vip_customers, round(avg(spend_usd)) AS avg_spend_usd FROM vip_spend;

-- Transactions and MVCC: after ROLLBACK the delete never happened.
-- DuckDB transactions behave like SNAPSHOT isolation in SQL Server.
BEGIN TRANSACTION;
DELETE FROM vip_spend WHERE spend_usd < 20000;
SELECT count(*) AS rows_inside_transaction FROM vip_spend;
ROLLBACK;
SELECT count(*) AS rows_after_rollback FROM vip_spend;

-- The warehouse itself cannot be changed from here: it is attached READ_ONLY,
-- and the ETL replaces it with a new file instead. Remove the -- to see the error:
-- DELETE FROM dw.fact_orders WHERE order_id = 1;
