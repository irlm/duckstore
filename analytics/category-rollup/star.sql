-- Subtotals per department and a grand total with ROLLUP; the tree is already flattened.
SELECT dp.category_l1                AS level1,
       dp.category_l2                AS level2,
       count(DISTINCT fs.order_id)   AS orders,
       sum(fs.net_usd)               AS revenue_usd
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
GROUP BY ROLLUP (dp.category_l1, dp.category_l2)
ORDER BY level1 NULLS LAST, level2 NULLS FIRST
