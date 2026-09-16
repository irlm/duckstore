WITH fx_daily AS MATERIALIZED (
    -- Postgres has no ASOF JOIN. Build one exchange rate per currency per calendar
    -- day (weekends carry the last business day's rate), then join on (currency, day).
    SELECT c.code AS currency_code, d::date AS day,
           (SELECT f.units_per_usd
              FROM store.fx_rates f
             WHERE f.currency_code = c.code AND f.rate_date <= d::date
             ORDER BY f.rate_date DESC
             LIMIT 1) AS units_per_usd
    FROM store.currencies c
    CROSS JOIN generate_series((SELECT min(placed_at)::date FROM store.orders),
                               (SELECT max(placed_at)::date FROM store.orders),
                               interval '1 day') AS g(d)
),
per_customer AS (
    SELECT o.customer_id,
           (SELECT max(placed_at)::date FROM store.orders WHERE id <= @max_order_id) - max(o.placed_at::date) AS recency_days,
           count(*)                                        AS frequency,
           sum(round(o.total / fx.units_per_usd, 2))       AS monetary
    FROM store.orders o
    JOIN fx_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
    WHERE o.status <> 'cancelled' AND o.id <= @max_order_id
    GROUP BY o.customer_id
),
scored AS (
    SELECT *,
           ntile(5) OVER (ORDER BY recency_days DESC, customer_id) AS r,
           ntile(5) OVER (ORDER BY frequency, customer_id)         AS f
    FROM per_customer
)
SELECT CASE WHEN r >= 4 AND f >= 4 THEN 'Champions'
            WHEN f >= 4            THEN 'Loyal'
            WHEN r <= 2 AND f >= 3 THEN 'At risk'
            WHEN r >= 4            THEN 'Recent, occasional'
            WHEN r <= 2            THEN 'Hibernating'
            ELSE 'Needs attention' END  AS segment,
       count(*)                          AS customers,
       round(avg(frequency), 2)          AS avg_orders,
       round(avg(monetary), 2)           AS avg_spend_usd
FROM scored
GROUP BY 1
ORDER BY customers DESC, segment
