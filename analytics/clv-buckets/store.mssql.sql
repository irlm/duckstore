-- Lifetime spend per customer, then the customers grouped into buckets.
-- T-SQL cannot group by a column alias, so the buckets are named in their own CTE.
WITH spend AS (
    SELECT o.customer_id,
           sum(CAST(round(o.total / fx.units_per_usd, 2) AS decimal(14, 2))) AS lifetime_usd
    FROM store.orders o
    JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = CAST(o.placed_at AS date)
    WHERE o.status <> 'cancelled' AND o.id <= @max_order_id
    GROUP BY o.customer_id
),
bucketed AS (
    SELECT CASE WHEN lifetime_usd < 100  THEN '1. under $100'
                WHEN lifetime_usd < 250  THEN '2. $100 to $250'
                WHEN lifetime_usd < 500  THEN '3. $250 to $500'
                WHEN lifetime_usd < 1000 THEN '4. $500 to $1,000'
                WHEN lifetime_usd < 2500 THEN '5. $1,000 to $2,500'
                WHEN lifetime_usd < 5000 THEN '6. $2,500 to $5,000'
                ELSE '7. $5,000 or more' END AS bucket,
           lifetime_usd
    FROM spend
)
SELECT bucket,
       count(*)                                           AS customers,
       round(100.0 * count(*) / sum(count(*)) OVER (), 2) AS share_pct,
       sum(lifetime_usd)                                  AS revenue_usd
FROM bucketed
GROUP BY bucket
ORDER BY bucket
