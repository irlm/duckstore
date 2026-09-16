WITH delivered AS (
    SELECT carrier, (epoch(delivered_at) - epoch(shipped_at)) / 86400.0 AS days
    FROM dw.fact_orders
    WHERE delivered_at IS NOT NULL
)
SELECT carrier,
       count(*)                                                     AS deliveries,
       round(quantile_cont(days, 0.5), 2)                           AS p50_days,
       round(quantile_cont(days, 0.9), 2)                           AS p90_days,
       round(100.0 * count(*) FILTER (WHERE days > 7) / count(*), 2) AS over_7_days_pct
FROM delivered
GROUP BY carrier
ORDER BY p90_days DESC, carrier
