-- Revenue per month: the ETL already converted every line to USD (net_usd).
SELECT date_trunc('month', order_date)::DATE AS month,
       count(DISTINCT order_id)             AS orders,
       sum(net_usd)                         AS revenue_usd
FROM dw.fact_sales
GROUP BY 1
ORDER BY 1
