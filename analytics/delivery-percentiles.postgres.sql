WITH last_shipment AS (
    -- the parcel that arrived last decides when the order was delivered
    SELECT DISTINCT ON (s.order_id) s.carrier, s.shipped_at, s.delivered_at
    FROM store.shipments s
    WHERE s.order_id <= @max_order_id
    ORDER BY s.order_id, s.delivered_at DESC NULLS FIRST, s.shipped_at DESC NULLS FIRST
),
delivered AS (
    SELECT carrier, extract(epoch FROM delivered_at - shipped_at)::float8 / 86400.0 AS days
    FROM last_shipment
    WHERE delivered_at IS NOT NULL
)
SELECT carrier,
       count(*)                                                                AS deliveries,
       round(percentile_cont(0.5) WITHIN GROUP (ORDER BY days)::numeric, 2)    AS p50_days,
       round(percentile_cont(0.9) WITHIN GROUP (ORDER BY days)::numeric, 2)    AS p90_days,
       round(100.0 * count(*) FILTER (WHERE days > 7) / count(*), 2)           AS over_7_days_pct
FROM delivered
GROUP BY carrier
ORDER BY p90_days DESC, carrier
