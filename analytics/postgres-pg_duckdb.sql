-- pg_duckdb: DuckDB's execution engine inside Postgres (https://github.com/duckdb/pg_duckdb).
-- `make docker-pgduckdb` runs this file. The Postgres image (docker/postgres.Dockerfile) contains the
-- extension, and docker-compose.yml loads it at server start (shared_preload_libraries = pg_duckdb).
--
-- A query runs in DuckDB when it uses DuckDB-only features (read_parquet, MotherDuck tables...) or when
-- the session says so:
--   SET duckdb.force_execution = true;
-- DuckDB then reads the Postgres tables itself, page by page, and runs the joins and aggregations with its
-- vectorized engine on several threads. The data stays in Postgres row storage: that is the difference
-- from the analytics service, which reads its own column store file.
--
-- The Compare page's pg_duckdb approaches connect with these session settings (Npgsql "Options"):
--   duckdb.force_execution = true
--   duckdb.threads_for_postgres_scan = 8      default 2: threads DuckDB uses to read one Postgres table
--   duckdb.max_workers_per_postgres_scan = 8  default 2: Postgres workers that feed those threads
-- With the defaults, reading the store tables was the bottleneck (unusual days: 1.46 s with 2, 0.91 s with 8).
-- Memory: duckdb.max_memory (default 4GB) applies to every Postgres connection that runs DuckDB.

CREATE EXTENSION IF NOT EXISTS pg_duckdb;

SELECT extversion AS pg_duckdb_version FROM pg_extension WHERE extname = 'pg_duckdb';
