-- title: 02 · Window functions and QUALIFY
-- engine: duckdb
--
-- Window functions work like in SQL Server. DuckDB adds QUALIFY, named
-- windows, and aggregates like arg_max that remove many self-joins.

-- Top 3 products per department by revenue.
-- T-SQL: WITH r AS (SELECT ..., ROW_NUMBER() OVER (...) AS rn FROM ...) SELECT * FROM r WHERE rn <= 3
-- DuckDB: QUALIFY filters on the window function, no CTE needed.
SELECT dp.category_l1                                                          AS department,
       dp.product_name,
       round(sum(fs.net_usd))                                                  AS revenue_usd,
       row_number() OVER (PARTITION BY dp.category_l1 ORDER BY sum(fs.net_usd) DESC) AS rank_in_department
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
GROUP BY dp.category_l1, dp.product_id, dp.product_name
QUALIFY rank_in_department <= 3
ORDER BY department, rank_in_department;

-- Running total and month-over-month growth with LAG.
WITH monthly AS (
    SELECT date_trunc('month', order_date) AS month, sum(net_usd) AS revenue
    FROM dw.fact_sales
    GROUP BY ALL
)
SELECT month,
       round(revenue)                                                   AS revenue_usd,
       round(sum(revenue) OVER (ORDER BY month))                        AS running_total_usd,
       round(100.0 * (revenue / lag(revenue) OVER (ORDER BY month) - 1), 1) AS growth_pct
FROM monthly
ORDER BY month;

-- A named WINDOW used by two functions: a 7-day moving average and maximum.
SELECT order_date,
       count(*)                         AS orders,
       round(avg(count(*)) OVER w7, 1)  AS avg_7d,
       max(count(*)) OVER w7            AS max_7d
FROM dw.fact_orders
GROUP BY order_date
WINDOW w7 AS (ORDER BY order_date ROWS BETWEEN 6 PRECEDING AND CURRENT ROW)
ORDER BY order_date DESC
LIMIT 14;

-- Days between a customer's consecutive orders (LAG per customer), as quartiles.
WITH gaps AS (
    SELECT date_diff('day',
                     lag(order_date) OVER (PARTITION BY customer_id ORDER BY placed_at),
                     order_date) AS days_since_previous
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
)
SELECT quantile_cont(days_since_previous, [0.25, 0.5, 0.75, 0.9]) AS quartiles_days,
       count(days_since_previous)                                AS repeat_orders
FROM gaps;

-- arg_max(x, y): the x of the row where y is the highest. No window and no join back.
SELECT dc.country,
       max(fo.total_usd)                  AS biggest_order_usd,
       arg_max(fo.order_id, fo.total_usd) AS biggest_order_id,
       arg_max(dc.full_name, fo.total_usd) AS placed_by
FROM dw.fact_orders fo
JOIN dw.dim_customer dc USING (customer_id)
GROUP BY ALL
ORDER BY biggest_order_usd DESC;

-- ntile(10): customer spend deciles. How concentrated is revenue?
WITH spend AS (
    SELECT customer_id, sum(total_usd) AS lifetime_usd
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
    GROUP BY ALL
)
SELECT decile,
       count(*)                                                        AS customers,
       round(sum(lifetime_usd))                                        AS revenue_usd,
       round(100.0 * sum(lifetime_usd) / sum(sum(lifetime_usd)) OVER (), 1) AS share_pct
FROM (SELECT *, ntile(10) OVER (ORDER BY lifetime_usd DESC) AS decile FROM spend)
GROUP BY decile
ORDER BY decile;
