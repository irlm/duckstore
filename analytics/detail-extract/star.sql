SELECT fs.order_id, fs.line_no, fs.placed_at, fs.customer_id, fs.product_id, dp.product_name,
       fs.quantity, fs.currency_code, fs.unit_price_local, fs.net_usd
FROM dw.fact_sales fs
JOIN dw.dim_product dp USING (product_key)
WHERE fs.order_date = (SELECT max(order_date) FROM dw.fact_orders)
ORDER BY fs.order_id, fs.line_no
