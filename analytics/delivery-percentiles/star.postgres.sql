-- Postgres: extract(epoch FROM ...) instead of epoch(...), percentile_cont(...) WITHIN GROUP
-- instead of quantile_cont, and round() needs numeric, not double precision.
WITH delivered AS (
    SELECT carrier, (extract(epoch FROM delivered_at) - extract(epoch FROM shipped_at)) / 86400.0 AS days
    FROM dw.fact_orders
    WHERE delivered_at IS NOT NULL
)
SELECT carrier,
       count(*)                                                                 AS deliveries,
       round((percentile_cont(0.5) WITHIN GROUP (ORDER BY days))::numeric, 2)   AS p50_days,
       round((percentile_cont(0.9) WITHIN GROUP (ORDER BY days))::numeric, 2)   AS p90_days,
       round(100.0 * count(*) FILTER (WHERE days > 7) / count(*), 2)            AS over_7_days_pct
FROM delivered
GROUP BY carrier
ORDER BY p90_days DESC, carrier
