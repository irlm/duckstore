# Reports while the store is busy: Postgres, DuckDB and SQL Server

Customers keep shopping while somebody runs reports. The store traffic always goes to Postgres — that is the
application's database. Only the **reports** move: to the same Postgres, to the DuckDB analytics service, or to SQL
Server's columnstore star schema.

Measured 2026-09-19 on one laptop, 100 store operations per second for 60 s after a 5 s warm-up (70% product page,
20% order history, 10% checkout — a write transaction), with 2 report users running the questions one after the
other. Samples in [loadtest-scale5-laptop-sqlserver.csv](loadtest-scale5-laptop-sqlserver.csv).

```bash
make docker-loadtest ARGS="--rate 100 --duration 60 --report-users 2 --scenarios none,postgres,duckdb,sqlserver"
```

## What the store feels, p95 milliseconds

| operation | store only | + reports on Postgres | + reports on DuckDB service | + reports on SQL Server |
|---|---:|---:|---:|---:|
| product page | 2.2 | 5.4 | 5.5 | **3.4** |
| order history | 1.9 | 2.8 | 3.2 | **2.4** |
| checkout (a write) | 19.8 | 42.5 | 31.3 | **28.6** |

## What the reports get

| reports run on | finished in 60 s | per second | p50 | p95 |
|---|---:|---:|---:|---:|
| Postgres, store tables | 14 | 0.2 | 4,254 ms | 19,542 ms |
| SQL Server, star + columnstore | 124 | 2.1 | 757 ms | 2,603 ms |
| DuckDB service, star schema | **311** | **5.2** | **419 ms** | **663 ms** |

## What it says

**Reporting on the OLTP database is the worst of both.** Fourteen reports in a minute, and checkout p95 more than
doubles (19.8 → 42.5 ms) because the reports take the same CPU, cache and locks the customers need.

**Moving the reports to their own engine helps both sides.** SQL Server answered 8.9× more reports than Postgres and
DuckDB 22× more, and in both cases the store stayed faster than it was with reports on Postgres.

**SQL Server disturbed the store least** (checkout p95 28.6 ms against DuckDB's 31.3 ms), while **DuckDB answered the
reports fastest** (419 ms against 757 ms at p50, and 663 ms against 2.6 s at p95). Both are honest results for one
laptop where all three engines share 16 threads: DuckDB uses every core it can get for each query, which is why its
reports finish first and why the store notices it slightly more.

**The shape of the answer does not depend on the engine you pick, only on separating the work.** On a real lab, with
the reporting engine on its own machine, the store's numbers should come back down to the "store only" column — that
is what [lab/](../../lab) is for.

## Setup

- AMD Ryzen 7 5825U, 16 threads, 28 GB; Postgres `shared_buffers` 3 GB, DuckDB `memory_limit` 6 GB, SQL Server
  `max server memory` 5 GB — all on the same machine.
- SQL Server ran the star schema with a clustered columnstore plus the lookup b-tree of
  [analytics/mssql-star-index.sql](../../analytics/mssql-star-index.sql).
- Report questions: brand returns, active customers, sales hierarchy, unusual days, revenue per month.
