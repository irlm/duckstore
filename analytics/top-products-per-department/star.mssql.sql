-- The warehouse already flattened the category tree into columns, so there is no recursion.
SELECT department, [rank], product_id, product_name, revenue_usd
FROM (
    SELECT dp.category_l1                                    AS department,
           row_number() OVER (PARTITION BY dp.category_l1
                              ORDER BY sum(fs.net_usd) DESC, fs.product_id) AS [rank],
           fs.product_id,
           max(dp.product_name)                              AS product_name,
           sum(fs.net_usd)                                   AS revenue_usd
    FROM dw.fact_sales fs
    JOIN dw.dim_product dp ON dp.product_key = fs.product_key
    WHERE fs.order_date > DATEADD(day, -365, (SELECT max(order_date) FROM dw.fact_orders))
    GROUP BY dp.category_l1, fs.product_id
) ranked
WHERE [rank] <= 3
ORDER BY department, [rank]
