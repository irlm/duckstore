SELECT TOP (15)
       dp.brand,
       sum(fs.quantity)                                                   AS units_sold,
       coalesce(sum(fr.quantity), 0)                                      AS units_returned,
       round(100.0 * coalesce(sum(fr.quantity), 0) / sum(fs.quantity), 2) AS return_rate_pct
FROM dw.fact_sales fs
JOIN dw.dim_product dp ON dp.product_key = fs.product_key
LEFT JOIN dw.fact_returns fr ON fr.order_id = fs.order_id AND fr.line_no = fs.line_no
WHERE fs.order_status = 'delivered'
GROUP BY dp.brand
HAVING sum(fs.quantity) > 1000
ORDER BY return_rate_pct DESC, dp.brand
