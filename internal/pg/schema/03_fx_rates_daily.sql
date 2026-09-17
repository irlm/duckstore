-- =============================================================================
-- fx_rates_daily: one exchange rate per currency per calendar day
--
-- fx_rates has a rate per business day. Reports need the rate of the day an
-- order was placed, and a weekend uses Friday's rate. The first version of the
-- Compare queries computed that in every query, in a MATERIALIZED CTE. A CTE
-- has no statistics (like a table variable in SQL Server), so Postgres guessed
-- that joining 12M orders to it returns 60,000 rows and chose a serial nested
-- loop: revenue per month took 42 s. With this table and its statistics the
-- plan is a parallel hash join and the same query takes 17 s.
--
-- The seed builds it after the constraints. It is re-runnable, so after loading
-- new rates (or on a database seeded before this file existed) run:
--   docker compose exec -T postgres psql -U store -d store -f - < internal/pg/schema/03_fx_rates_daily.sql
-- The last known rate is carried forward for a year, so new orders find a rate.
-- =============================================================================

SET search_path = store;

DROP TABLE IF EXISTS fx_rates_daily;

CREATE TABLE fx_rates_daily AS
SELECT c.code    AS currency_code,
       g.d::date AS day,
       (SELECT f.units_per_usd                 -- T-SQL: OUTER APPLY (SELECT TOP 1 ... ORDER BY rate_date DESC)
          FROM fx_rates f
         WHERE f.currency_code = c.code AND f.rate_date <= g.d::date
         ORDER BY f.rate_date DESC
         LIMIT 1) AS units_per_usd
FROM currencies c
CROSS JOIN generate_series((SELECT min(rate_date) FROM fx_rates),
                           (SELECT max(rate_date) FROM fx_rates) + 365,
                           interval '1 day') AS g(d);

DELETE FROM fx_rates_daily WHERE units_per_usd IS NULL;   -- days before a currency's first rate

ALTER TABLE fx_rates_daily ADD PRIMARY KEY (currency_code, day);
ALTER TABLE fx_rates_daily ADD FOREIGN KEY (currency_code) REFERENCES currencies (code);
ANALYZE fx_rates_daily;
