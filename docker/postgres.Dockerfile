# Postgres 18 with the pg_duckdb extension: DuckDB's engine inside Postgres.
#
# The pg_duckdb project publishes pgduckdb/pgduckdb images, but that image is Postgres 18.1 on
# Debian 12, while the database volume was created by the official postgres:18 image (Debian 13).
# A different C library can sort text differently, which can break text indexes. So this image
# keeps the official Postgres and copies only the extension files in.
FROM pgduckdb/pgduckdb:18-v1.1.1 AS pgduckdb

FROM postgres:18
RUN apt-get update \
 && apt-get install -y --no-install-recommends libcurl4t64 liblz4-1 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=pgduckdb /usr/lib/postgresql/18/lib/pg_duckdb.so /usr/lib/postgresql/18/lib/libduckdb.so /usr/lib/postgresql/18/lib/
COPY --from=pgduckdb /usr/share/postgresql/18/extension/pg_duckdb* /usr/share/postgresql/18/extension/
