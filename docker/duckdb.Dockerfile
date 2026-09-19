# The DuckDB command line tool, for machines that do not have it installed.
#
# The benchmark asks each engine for its own timing, which means running the real client.
# This image is that client, and it can read the warehouse volume directly:
#
#   docker build -f docker/duckdb.Dockerfile -t duckstore-duckdb .
#   docker run --rm -i -v duckstore_warehouse:/data duckstore-duckdb -readonly /data/warehouse.duckdb
#
# Works on x86_64 and on arm64 (Raspberry Pi, Apple Silicon), which is why the architecture
# comes from the build platform instead of being hardcoded.
FROM debian:13-slim

ARG DUCKDB_VERSION=1.5.5
ARG TARGETARCH

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip \
 && rm -rf /var/lib/apt/lists/* \
 && case "${TARGETARCH}" in \
      amd64) asset=duckdb_cli-linux-amd64.zip ;; \
      arm64) asset=duckdb_cli-linux-arm64.zip ;; \
      *) echo "no DuckDB CLI for ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && curl -fsSL -o /tmp/duckdb.zip "https://github.com/duckdb/duckdb/releases/download/v${DUCKDB_VERSION}/${asset}" \
 && unzip -q /tmp/duckdb.zip -d /usr/local/bin \
 && rm /tmp/duckdb.zip \
 && chmod +x /usr/local/bin/duckdb \
 && apt-get purge -y curl unzip && apt-get autoremove -y

ENTRYPOINT ["duckdb"]
