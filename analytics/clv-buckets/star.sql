WITH spend AS (
    SELECT customer_id, sum(total_usd) AS lifetime_usd
    FROM dw.fact_orders
    WHERE status <> 'cancelled'
    GROUP BY customer_id
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
GROUP BY bucket
ORDER BY bucket
