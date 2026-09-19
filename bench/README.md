# bench — engine benchmarks for the lab

The Compare page measures what an **application** sees: a connection, a query, rows read and JSON written. These
scripts measure the **engine** alone, on lab machines: every engine reports its own time (DuckDB `.timer`, psql
`\timing`, sqlcmd `SET STATISTICS TIME`), all repetitions run inside one process, and the first runs are warm-up.

They are built to fit [NetworkLab](https://github.com/irlm/NetworkLab)'s `duckbench` lab, which already runs TPC-H on
real machines with Prometheus and Grafana watching. Same script shape, same result files, so both sets of numbers can
sit in one table.

## What this adds to the TPC-H benchmark

| duckbench (TPC-H) | this |
|---|---|
| 5 queries: scans and joins | 15 business questions: lookups, large results, JSON attributes, a recursive hierarchy, percentiles, cohorts |
| One fixed schema | The same question on **two data models**: store tables (3NF) and star schema |
| No indexes, by design | The indexes a production database would have, and SQL Server **columnstore** |
| Load once | ETL cost and data freshness: full and incremental |
| Reports only | Store traffic (checkouts) **and** reports at the same time |
| SQL written per engine by hand | SQL exported from the application, so the benchmark runs what the app runs |

## The SQL comes from the application

`export-sql` writes every question as a ready-to-run file, with the parameters replaced by literals, one folder per
engine and data model:

```bash
docker compose run --rm --no-deps analytics export-sql --out /data/bench-sql --engines postgres,duckdb
docker cp duckstore-analytics:/data/bench-sql ./bench/sql
```

```
bench/sql/PARAMS                      watermark and customer id used
bench/sql/postgres/store/*.sql        the SQL the Compare page runs on Postgres
bench/sql/postgres/star/*.sql
bench/sql/duckdb/store/*.sql          the same questions, rewritten for the raw.* copy
bench/sql/duckdb/star/*.sql
bench/sql/mssql/…                     only where a T-SQL file exists (analytics/<id>/<model>.mssql.sql)
```

A dialect never falls back to another engine's SQL: SQL Server without its own file is reported as missing, not
benchmarked with Postgres SQL.

## Running

```bash
# every question, both models, on the engine that owns this machine
./duckstore-bench-run.sh --engine postgres --sql-dir ./sql --repeat 3
./duckstore-bench-run.sh --engine duckdb   --sql-dir ./sql --duckdb-file /data/warehouse.duckdb
./duckstore-bench-run.sh --engine pgduckdb --sql-dir ./sql --model store

# what a query costs when nothing is cached (restarts Postgres, drops the page cache)
./duckstore-bench-run.sh --engine postgres --sql-dir ./sql --cold --repeat 2
```

Passwords come from the environment or `/etc/lab-secrets.env` (mode 600), never from a flag: `PGPASSWORD`,
`SQLCMDPASSWORD`, `LAB_SQL_SA_PASSWORD` for the cold runs on SQL Server.

`bench.conf` next to these scripts (git-ignored) holds the host names and paths of your lab, so the flags stay short.
See `bench.conf.example`.

## Results

One row per timed run, tab separated, no header, appended to `~/results-duckstore-<engine>-<stamp>.tsv`:

```
iso_time              host   engine    scale  query                    rep  ms
2026-09-19T14:36:13Z  reef   postgres  5      unusual-days.store       1    2079.835
```

That is duckbench's 7-column shape, in the home directory with the `results-*.tsv` name, so its collector gathers
these runs together with the TPC-H ones:

```bash
./duckbench-collect.sh --label duckstore-sf5 --hosts duck@lab-db,duck@lab-client
```

Analyze them with DuckDB:

```sql
SELECT engine, query, median(ms) AS ms
FROM read_csv('results-*.tsv', delim='\t', header=false,
              columns={'t':'TIMESTAMP','host':'VARCHAR','engine':'VARCHAR','scale':'INT',
                       'query':'VARCHAR','rep':'INT','ms':'DOUBLE'})
GROUP BY ALL ORDER BY query, ms;
```

## Conventions

The same ones NetworkLab's lab scripts use, because the same people read both:

- `set -euo pipefail`, `SCRIPT_DIR` + `lib/common.sh`, `usage`, `--dry-run`, `log/ok/warn/die`.
- Every script ends with the source guard `[[ "${BASH_SOURCE[0]}" == "$0" ]] && main "$@"`, so tests can source it and
  call its helpers.
- Pure helpers (parsing, medians, file lists) are separate from the part that talks to engines, and are unit-tested
  against real client output.
- A cold run that could not empty the caches fails; it is never reported as if it were cold.

```bash
bash tests/run-all.sh     # syntax, shellcheck, unit tests — no root, no database
```

## Next

- T-SQL for the 15 questions (`analytics/<id>/<model>.mssql.sql`) and a loader into SQL Server, with a clustered
  columnstore on the star schema.
- `duckstore-bench-verify.sh`: prove every engine returns the same answer before any timing is trusted.
- A fleet runner for the mixed test: store traffic from one machine, reports from others, synchronised start.
