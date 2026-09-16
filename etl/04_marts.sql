-- =============================================================================
-- ETL step 4: MARTS (pre-computed answers the store app reads)
--
-- "Frequently bought together" is a self-join of every order line with every
-- other line of the same order: millions of pairs. That is a bad job for the
-- OLTP database on every page view, and a quick batch job for DuckDB.
-- The store's product page reads this small table instead.
-- =============================================================================

-- step: dw.product_pairs (frequently bought together)
CREATE TABLE dw.product_pairs AS
SELECT
    a.product_id,
    b.product_id AS other_product_id,
    count(*)     AS orders_together
FROM dw.fact_sales a
JOIN dw.fact_sales b
  ON b.order_id = a.order_id
 AND b.product_id <> a.product_id
-- Note: GROUP BY ALL would be shorter, but DuckDB does not (yet) allow it
-- together with QUALIFY, so the columns are listed.
GROUP BY a.product_id, b.product_id
-- QUALIFY filters on a window function, like HAVING filters on an aggregate.
-- T-SQL needs a CTE: WITH x AS (SELECT ..., ROW_NUMBER() ...) SELECT ... WHERE rn <= 8
QUALIFY row_number() OVER (PARTITION BY a.product_id ORDER BY orders_together DESC, other_product_id) <= 8
ORDER BY product_id;

-- step: dw.product_stats (rating and sales per product)
CREATE TABLE dw.product_stats AS
SELECT
    p.product_id,
    coalesce(s.units_90d, 0)      AS units_90d,
    coalesce(s.revenue_90d, 0)    AS revenue_usd_90d,
    r.review_count,
    r.avg_rating
FROM (SELECT DISTINCT product_id FROM dw.dim_product) p
LEFT JOIN (
    SELECT product_id, sum(quantity) AS units_90d, sum(net_usd) AS revenue_90d
    FROM dw.fact_sales
    WHERE order_date > (SELECT max(order_date) FROM dw.fact_sales) - INTERVAL 90 DAY
    GROUP BY ALL
) s USING (product_id)
LEFT JOIN (
    SELECT product_id, count(*) AS review_count, round(avg(rating), 2) AS avg_rating
    FROM dw.fact_reviews
    GROUP BY ALL
) r USING (product_id);
