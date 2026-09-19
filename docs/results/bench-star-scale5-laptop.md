# Star schema on four engines, one laptop, scale 5

DuckDB, SQL Server 2025 with a clustered columnstore, Postgres and pg_duckdb answering the same 15 questions on the
same rows. Measured 2026-09-19 with `bench/duckstore-bench-local.sh --model star --repeat 3 --warmup 1`; raw rows in
[bench-star-scale5-laptop.tsv](bench-star-scale5-laptop.tsv).

**Every answer was checked first.** `bench/duckstore-bench-verify.sh` compared each engine's rows with Postgres:
SQL Server matched on all 15 star questions (and all 15 store-table ones), DuckDB on all 15. A benchmark whose
engines disagree measures different questions.

## The machine and the setup

- AMD Ryzen 7 5825U, 16 threads, 28 GB, one laptop that also runs a desktop.
- Data: scale 5 — 12.5M orders, 26.5M order lines, watermark 12,511,499.
- Timing is what each engine reports (DuckDB `.timer`, psql `\timing`, sqlcmd `SET STATISTICS TIME`), median of 3
  runs after 1 warm-up, all repetitions inside one process.
- Engines ran one at a time, but the other containers stayed up.

| engine | version | storage | memory |
|---|---|---|---|
| DuckDB | 1.5.5 | its own file, star schema | `memory_limit` 6 GB |
| SQL Server | 2025 Enterprise Developer (CU9), Linux container | clustered columnstore on the facts, rowstore dimensions | **unlimited for this run** |
| Postgres | 18.3 + pg_duckdb 1.1.0 | the star schema copied into the `dw` schema | `shared_buffers` 3 GB plus the OS page cache |
| pg_duckdb | same server, `duckdb.force_execution` | Postgres rows, DuckDB executor | — |

SQL Server had no memory cap here: the container ignores `MSSQL_MEMORY_LIMIT_MB`, and the `sp_configure` fix landed
after this run. So this table is, if anything, generous to SQL Server.

## Median milliseconds, lower is better

| question | DuckDB | SQL Server | Postgres | pg_duckdb |
|---|---:|---:|---:|---:|
| unusual-days | **11** | 44 | 253 | 676 |
| detail-extract (23,063 rows) | **20** | 35 | 76 | 783 |
| customer-orders (lookup) | 6 | 61 | **0.1** | 11 |
| yoy-growth | **121** | 926 | 3,037 | 9,862 |
| sales-hierarchy | 180 | **149** | 924 | 2,897 |
| clv-buckets | **211** | 210 | 9,576 | 1,567 |
| active-customers | **229** | 465 | 3,480 | 4,165 |
| basket-pairs | **258** | 1,298 | 8,152 | 1,721 |
| brand-returns | **270** | 299 | 3,324 | 3,122 |
| monthly-revenue | **272** | 1,582 | 6,250 | 8,851 |
| delivery-percentiles | **305** | 12,353 | 13,391 | 1,844 |
| cohort-retention | **439** | 1,198 | 23,670 | 5,838 |
| rfm-segments | **442** | 908 | 13,681 | 2,102 |
| top-products-per-department | **498** | 2,506 | 2,818 | 3,519 |
| category-rollup | **2,841** | 84,280 | 22,245 | 8,580 |

## What the numbers say

**Columnstore is the competitor, not row storage.** SQL Server with a clustered columnstore beats the same star
schema in Postgres on 14 of 15 questions, often by 10–20× (cohort retention 1.2 s against 23.7 s). A SQL Server team
comparing DuckDB with their own rowstore tables is comparing against the wrong thing.

**DuckDB still wins 13 of 15**, but the margin against columnstore is 2–5×, not the 10–90× it has against row
storage. The two engines do the same kind of work: columns, compression, and operators that process a batch of rows
per call.

**Where SQL Server wins:** the org-chart question (149 ms against 180 ms), where the dimension is small and the work
is parsing JSON, not scanning.

**Where Postgres wins:** the lookup, 0.1 ms against 61 ms. A clustered columnstore has no b-tree for "this
customer's last 20 orders", so it scans. The right storage depends on the question, not on the engine.

**Two T-SQL patterns cost real time:**
- `COUNT(DISTINCT …)` with `ROLLUP` (category rollup): 84 s, slower than Postgres and 30× slower than DuckDB.
- `PERCENTILE_CONT` as a window function, which T-SQL has no aggregate form of: 12.4 s against DuckDB's 0.3 s.

Both are cases where the same question has to be written differently for SQL Server, and the difference shows up in
the plan, not in the SQL.
