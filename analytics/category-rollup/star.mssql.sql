-- The tree is already flattened into columns, so ROLLUP is all that is left.
SELECT dp.category_l1                AS level1,
       dp.category_l2                AS level2,
       count(DISTINCT fs.order_id)   AS orders,
       sum(fs.net_usd)               AS revenue_usd
FROM dw.fact_sales fs
JOIN dw.dim_product dp ON dp.product_key = fs.product_key
GROUP BY ROLLUP (dp.category_l1, dp.category_l2)
ORDER BY CASE WHEN dp.category_l1 IS NULL THEN 1 ELSE 0 END, dp.category_l1, dp.category_l2
