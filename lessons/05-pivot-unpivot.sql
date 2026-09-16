-- title: 05 · PIVOT and UNPIVOT without dynamic SQL
-- engine: duckdb
--
-- In T-SQL, PIVOT needs the list of values written in the query, so a
-- pivot on "every year" or "every payment method" needs dynamic SQL.
-- DuckDB reads the distinct values first and builds the columns itself.

-- Orders per payment method per year: one column per method.
PIVOT (
    SELECT year(order_date) AS year, payment_method
    FROM dw.fact_orders
    WHERE payment_method IS NOT NULL
)
ON payment_method
USING count(*)
GROUP BY year
ORDER BY year;

-- Two aggregates at once: the columns are named <value>_<alias>.
PIVOT (
    SELECT dp.category_l1 AS department, year(fs.order_date) AS year, fs.net_usd, fs.quantity
    FROM dw.fact_sales fs
    JOIN dw.dim_product dp USING (product_key)
)
ON year
USING round(sum(net_usd)) AS revenue, sum(quantity) AS units
GROUP BY department
ORDER BY department;

-- Only some values, and the table name directly after PIVOT.
PIVOT dw.fact_orders
ON status IN ('delivered', 'shipped', 'cancelled')
USING count(*)
GROUP BY currency_code
ORDER BY currency_code;

-- UNPIVOT turns columns into rows. COLUMNS(* EXCLUDE ...) picks the columns
-- without typing them: here, the money parts of one order.
UNPIVOT (
    SELECT order_id, subtotal_usd, discount_usd, shipping_usd, tax_usd, total_usd
    FROM dw.fact_orders
    WHERE order_id = 1000
)
ON COLUMNS(* EXCLUDE order_id)
INTO NAME component VALUE usd;

-- Pivot, then unpivot back: price versions created per year, per department.
WITH wide AS (
    PIVOT (SELECT category_l1, year(valid_from) AS year FROM dw.dim_product)
    ON year
    USING count(*)
    GROUP BY category_l1
)
UNPIVOT wide
ON COLUMNS(* EXCLUDE category_l1)
INTO NAME year VALUE price_versions
ORDER BY category_l1, year;
