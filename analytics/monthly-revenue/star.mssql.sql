-- Revenue per month: the ETL already converted every line to USD (net_usd).
SELECT CAST(DATETRUNC(month, order_date) AS date) AS month,
       count(DISTINCT order_id)                   AS orders,
       sum(net_usd)                               AS revenue_usd
FROM dw.fact_sales
GROUP BY CAST(DATETRUNC(month, order_date) AS date)
ORDER BY 1
