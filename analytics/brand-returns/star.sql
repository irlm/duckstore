SELECT dp.brand,
       sum(fs.quantity)                                                   AS units_sold,
       coalesce(sum(fr.quantity), 0)                                      AS units_returned,
       round(100.0 * coalesce(sum(fr.quantity), 0) / sum(fs.quantity), 2) AS return_rate_pct
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
LEFT JOIN dw.fact_returns fr USING (order_id, line_no)
WHERE fs.order_status = 'delivered'
GROUP BY dp.brand
HAVING sum(fs.quantity) > 1000
ORDER BY return_rate_pct DESC, dp.brand
LIMIT 15
