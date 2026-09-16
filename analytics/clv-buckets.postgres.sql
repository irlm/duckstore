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
                               interval '1 day') AS d
),
spend AS (
    SELECT o.customer_id, sum(round(o.total / fx.units_per_usd, 2)) AS lifetime_usd
    FROM store.orders o
    JOIN fx_daily fx ON fx.currency_code = o.currency_code AND fx.day = o.placed_at::date
    WHERE o.status <> 'cancelled' AND o.id <= @max_order_id
    GROUP BY o.customer_id
)
SELECT CASE WHEN lifetime_usd < 100  THEN '1. under $100'
            WHEN lifetime_usd < 250  THEN '2. $100 to $250'
            WHEN lifetime_usd < 500  THEN '3. $250 to $500'
            WHEN lifetime_usd < 1000 THEN '4. $500 to $1,000'
            WHEN lifetime_usd < 2500 THEN '5. $1,000 to $2,500'
            WHEN lifetime_usd < 5000 THEN '6. $2,500 to $5,000'
            ELSE '7. $5,000 or more' END              AS bucket,
       count(*)                                        AS customers,
       round(100.0 * count(*) / sum(count(*)) OVER (), 2) AS share_pct,
       sum(lifetime_usd)                               AS revenue_usd
FROM spend
GROUP BY 1
ORDER BY 1
