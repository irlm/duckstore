-- Copy the star schema into Postgres (schema dw), so the Compare page can run the star-schema
-- SQL on Postgres too. Not part of the normal ETL: `dotnet DuckStore.Analytics.dll star-to-postgres`
-- (or `make docker-star`). Three files, in order:
--   1_before.postgres.sql  run by Postgres: an empty dw schema
--   2_copy.duckdb.sql      run by DuckDB: copy the tables from the warehouse file
--   3_after.postgres.sql   run by Postgres: keys, indexes, statistics

-- step: empty schema dw
DROP SCHEMA IF EXISTS dw CASCADE;
CREATE SCHEMA dw;
