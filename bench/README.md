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

One command, and it first checks what this machine can do — SQL Server has no ARM build, the
DuckDB CLI may be missing, Docker may not be usable by your user:

```bash
make bench-list                  # what this machine can run, and why not the rest
make bench-local                 # pick from a menu, then run
make bench-local ARGS="--yes --repeat 5"
```

```
  engine          note
  postgres   [x]  container duckstore-postgres
  pgduckdb   [x]  extension installed
  duckdb     [x]  warehouse volume, CLI in a container
  mssql      [ ]  SQL Server has no build for aarch64: run it on a lab machine
```

One engine at a time, for a lab machine that owns it:

```bash
./duckstore-bench-run.sh --engine postgres --sql-dir ./sql --repeat 3
./duckstore-bench-run.sh --engine duckdb   --sql-dir ./sql --duckdb-file /data/warehouse.duckdb
./duckstore-bench-run.sh --engine mssql    --sql-dir ./sql --mssql-host 127.0.0.1 --mssql-port 51433

# what a query costs when nothing is cached (restarts Postgres, drops the page cache)
./duckstore-bench-run.sh --engine postgres --sql-dir ./sql --cold --repeat 2
```

## Before timing: the same answer

```bash
./duckstore-bench-verify.sh --engine mssql            # against Postgres, every question
./duckstore-bench-verify.sh --engine duckdb --model star
```

Each question runs on both engines, both results are normalised (spaces, the NULL word,
decimals, timestamp formats) and compared line by line. A benchmark whose engines disagree is
measuring different questions.

## SQL Server

```bash
make docker-mssql            # SQL Server 2025 Developer, password written to .env
make docker-mssql-load       # the warehouse copied in: rowstore store tables, columnstore star
make docker-mssql-load ARGS="--no-columnstore"   # the same star schema as rowstore
make docker-mssql-down       # stop it and get the memory back
```

The loader builds the tables from the warehouse's own schema, so a new ETL column needs no
second definition, and gives each model the storage its engine is known for:

| | store schema | dw schema |
|---|---|---|
| storage | rowstore | clustered columnstore on the facts |
| keys | the same primary keys and indexes Postgres has | the dimension keys |
| why | neither engine gets a head start on the application's tables | the feature a SQL Server team would use for reports |

Passwords come from the environment or `/etc/lab-secrets.env` (mode 600), never from a flag: `PGPASSWORD`,
`SQLCMDPASSWORD`, `LAB_SQL_SA_PASSWORD` for the cold runs on SQL Server.

`bench.conf` next to these scripts (git-ignored) holds the host names and paths of your lab, so the flags stay short.
See `bench.conf.example`.

## Reading the numbers

Every engine is configured by its own convention, which is honest but not identical:

| engine | memory it gets on the laptop |
|---|---|
| Postgres | `shared_buffers` 3 GB, plus whatever the OS page cache holds (often 10 GB+) |
| SQL Server | `MSSQL_MEMORY_LIMIT_MB`, 4 GB by default, and it caches nothing outside that |
| DuckDB | `Warehouse__MemoryLimit`, 6 GB, and it spills to disk beyond that |

So a laptop run compares engines *as they are usually set up*, not with one budget. When a
question matters, run it again with the same limit for everyone and say so — that is test 1.2
in [lab/](../lab). SQL Server's limit is the one to raise first: with 4 GB the heavier
store-table questions spill to tempdb, which shows up as `PAGEIOLATCH` waits.

Other things that decide a number, in order of size: the **data model** (store tables vs star
schema), whether the answer is **warm or cold**, the **indexes** present, and only then the
engine.

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

- A fleet runner for the mixed test: store traffic from one machine, reports from others, synchronised start.
- The lab run: the same questions on lab machines, next to the TPC-H numbers (see `lab/`).
