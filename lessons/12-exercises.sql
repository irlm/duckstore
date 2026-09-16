-- title: 12 · Exercises (try them yourself)
-- engine: duckdb
--
-- Questions only, with hints. Write each query below its question and run.
-- Each one uses ideas from lessons 01-11. There is no single right answer.

-- 1. Which weekday has the highest average order value (USD)?
--    Hint: dayname(order_date), avg(total_usd), GROUP BY ALL.
SELECT 'write your query here' AS exercise_1;

-- 2. For each department (dim_product.category_l1), which brand has the
--    highest revenue in the last 12 months? One row per department.
--    Hint: GROUP BY department and brand, then QUALIFY row_number() ... = 1.

-- 3. What share of each promotion's orders came from NEW customers
--    (their first order)?
--    Hint: fact_orders.customer_order_number = 1, join dim_promotion.

-- 4. Build a PIVOT: rows = region (dim_customer.region), columns = segment,
--    values = revenue in USD millions.

-- 5. Which warehouse ships the slowest to customers in a DIFFERENT country?
--    Hint: fact_orders.warehouse_id -> dim_warehouse.country_code, compare
--    with dim_customer.country_code, then quantile_cont(transit_days, 0.9).

-- 6. Find products whose price went UP at least twice (dim_product versions),
--    with the list of prices in order as a LIST.
--    Hint: list(list_price_usd ORDER BY valid_from), then compare with lag().

-- 7. Postgres side (switch the engine): show the 5 customers with the most
--    orders in the last 30 days, then run EXPLAIN ANALYZE on your query.
--    Which index did Postgres use? Would a new index help?

-- 8. Place an order in the store, run the ETL, and find your order in
--    dw.fact_sales. Then change a product price and prove that, after the
--    next ETL, your old order still points to the old product version.
