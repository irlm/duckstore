-- Recency and frequency scores per customer, then customers grouped into segments.
-- AVG over an int or a decimal keeps that type in T-SQL, so both are widened before the
-- average; Postgres returns numeric by itself.
WITH per_customer AS (
    SELECT o.customer_id,
           DATEDIFF(day, max(CAST(o.placed_at AS date)),
                    (SELECT max(CAST(placed_at AS date)) FROM store.orders WHERE id <= @max_order_id)) AS recency_days,
           count(*)                                                       AS frequency,
           sum(CAST(round(o.total / fx.units_per_usd, 2) AS decimal(14, 2))) AS monetary
    FROM store.orders o
    JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = CAST(o.placed_at AS date)
    WHERE o.status <> 'cancelled' AND o.id <= @max_order_id
    GROUP BY o.customer_id
),
scored AS (
    SELECT customer_id, recency_days, frequency, monetary,
           ntile(5) OVER (ORDER BY recency_days DESC, customer_id) AS r,
           ntile(5) OVER (ORDER BY frequency, customer_id)         AS f
    FROM per_customer
),
segments AS (
    SELECT CASE WHEN r >= 4 AND f >= 4 THEN 'Champions'
                WHEN f >= 4            THEN 'Loyal'
                WHEN r <= 2 AND f >= 3 THEN 'At risk'
                WHEN r >= 4            THEN 'Recent, occasional'
                WHEN r <= 2            THEN 'Hibernating'
                ELSE 'Needs attention' END AS segment,
           frequency, monetary
    FROM scored
)
SELECT segment,
       count(*)                                        AS customers,
       round(avg(CAST(frequency AS decimal(19, 6))), 2) AS avg_orders,
       round(avg(CAST(monetary AS decimal(19, 6))), 2)  AS avg_spend_usd
FROM segments
GROUP BY segment
ORDER BY customers DESC, segment
