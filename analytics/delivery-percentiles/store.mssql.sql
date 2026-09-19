-- Delivery time percentiles per carrier.
-- Two T-SQL differences: DISTINCT ON becomes ROW_NUMBER, and PERCENTILE_CONT is a window
-- function here, not an aggregate, so it is computed per row and then folded with max().
-- NULLs also sort the other way round: Postgres puts them first in DESC, SQL Server last.
WITH ranked AS (
    SELECT s.carrier, s.shipped_at, s.delivered_at,
           row_number() OVER (PARTITION BY s.order_id
                              ORDER BY CASE WHEN s.delivered_at IS NULL THEN 0 ELSE 1 END, s.delivered_at DESC,
                                       CASE WHEN s.shipped_at IS NULL THEN 0 ELSE 1 END, s.shipped_at DESC) AS rn
    FROM store.shipments s
    WHERE s.order_id <= @max_order_id
),
delivered AS (
    SELECT carrier, CAST(DATEDIFF_BIG(microsecond, shipped_at, delivered_at) AS float) / 86400000000.0 AS days
    FROM ranked
    WHERE rn = 1 AND delivered_at IS NOT NULL
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
