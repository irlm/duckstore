-- The ETL already kept the last parcel per order, so there is nothing to deduplicate.
WITH delivered AS (
    SELECT carrier, CAST(DATEDIFF_BIG(microsecond, shipped_at, delivered_at) AS float) / 86400000000.0 AS days
    FROM dw.fact_orders
    WHERE delivered_at IS NOT NULL
),
pct AS (
    SELECT carrier, days,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY days) OVER (PARTITION BY carrier) AS p50,
           percentile_cont(0.9) WITHIN GROUP (ORDER BY days) OVER (PARTITION BY carrier) AS p90
    FROM delivered
)
SELECT carrier,
       count(*)                                                        AS deliveries,
       round(CAST(max(p50) AS decimal(19, 6)), 2)                      AS p50_days,
       round(CAST(max(p90) AS decimal(19, 6)), 2)                      AS p90_days,
       round(100.0 * sum(CASE WHEN days > 7 THEN 1 ELSE 0 END) / count(*), 2) AS over_7_days_pct
FROM pct
GROUP BY carrier
ORDER BY p90_days DESC, carrier
