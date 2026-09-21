# When to use DuckDB — and when SQL Server or Postgres is the better answer

What this project measured, turned into decisions. Written for a SQL Server developer: every engine behaviour is
mapped to the SQL Server idea it corresponds to, and every claim links to the run that measured it.

## The short answer

- **Keep the application on a row store** — Postgres or SQL Server. Lookups, small writes and many users writing at
  once are what they are built for, and DuckDB is not.
- **Give reports their own engine and their own CPU.** Running reports on the application's database is the worst
  option measured here. It gets the fewest reports done and hurts the store the most.
- **Reshape the data before you swap the engine.** A star schema made every engine 3–50× faster. No engine swap
  came close to that.
- **Then pick the reporting engine.**
  - DuckDB answers fastest.
  - SQL Server with a clustered columnstore is close behind, and it can serve lookups from the same table.
  - Postgres on row storage is far behind for reports.

## Four ways to put DuckDB in a system

| Pattern | Shape | Good for | Measured here |
|---|---|---|---|
| **1. An analytics service owns a DuckDB file** (this project) | app → Postgres → ETL → `warehouse.duckdb` ← API | One team; data that fits one machine; minutes-old data is fine | Full ETL 60 s at scale 5, 6 min at scale 20; incremental 11 s |
| **2. DuckDB over Parquet in object storage** (a data lake) | app → DB → CDC/ETL → Parquet on S3 ← many DuckDB readers | Many readers and tools sharing one history; cheap storage | Not yet |
| **3. DuckDB inside Postgres** (pg_duckdb), ideally on a read replica | app → primary → replica with pg_duckdb ← reports | Data seconds old, no ETL | 1.5–7× faster than Postgres on the same tables, but loses index lookups; 7 of 15 store questions did not finish in 10 min |
| **4. Not DuckDB: a warehouse server** (SQL Server columnstore, Fabric, ClickHouse, Snowflake…) | app → ETL → warehouse ← many users | Many concurrent users, many writers, security, high availability | SQL Server columnstore: 2–5× behind DuckDB, 10–20× ahead of Postgres |

## The checklist

| Question | Use DuckDB when… | Choose something else when… | Evidence |
|---|---|---|---|
| **What do the queries do?** | Scan and aggregate millions of rows, a few columns | Look up a few rows by key | Lookup: Postgres 0.1 ms, SQL Server with a b-tree under 1 ms, DuckDB 6 ms ([star](results/bench-star-scale5-laptop.md)) |
| **Who writes?** | One writer, in batches (an ETL) | Many users writing at the same time | 13% of concurrent DuckDB updates failed in the engine race ([README](../README.md)) |
| **How fresh must the data be?** | Minutes are fine | Seconds: pg_duckdb on a replica, a hybrid query, or CDC | Incremental ETL 11 s at scale 5 ([dotnet/README](../dotnet/README.md)) |
| **Where does it run?** | On its own cores or its own server | Next to the application's database, on shared CPU | Shared CPU: reports on Postgres doubled checkout p95; own cores: the store barely noticed ([load test](results/loadtest-scale5-laptop-separate-cores.md)) |
| **How many report users?** | A few to tens | Hundreds at once | **Not measured yet** — the concurrency sweep is next |
| **How big are the results?** | Small, aggregated answers | Large extracts sent as JSON | JSON was most of the time for a 42k-row result ([dotnet/README](../dotnet/README.md)) |
| **How much data?** | Fits one machine's disk (it spills past memory) | Terabytes, or growing past one node | Scale 5 = 2.8 GB file; scale 20 = 10.3 GB |
| **Operations and security?** | A rebuildable copy, access through a service | High availability, point-in-time recovery, row-level security inside the database | By design: a DuckDB file has no users and no log shipping |

## What was measured, in SQL Server terms

All at scale 5 on one laptop unless marked: 12.5M orders and 26.5M order lines. Every answer was checked against
Postgres before any timing.

### 1. Columnstore is the real competitor, not row storage

On the star schema:
- **SQL Server with a clustered columnstore beat the same schema in Postgres on 14 of 15 questions**, often by 10–20×.
  Cohort retention took 1.2 s against 23.7 s.
- **DuckDB won 13 of 15**, but only 2–5× ahead of columnstore, against 10–90× ahead of row storage.

The two engines work the same way: they keep each column apart and compressed, and their operators process a batch
of rows per call. In SQL Server terms, DuckDB is batch mode on a columnstore, all the time.
([star results](results/bench-star-scale5-laptop.md))

> **For a SQL Server team:** compare DuckDB with your columnstore, not with your rowstore tables. Against rowstore
> the numbers look like magic; against columnstore they are a fair fight that DuckDB usually wins.

### 2. The data model beats the engine

Revenue per month, store tables → star schema:

| engine | store tables | star schema |
|---|---:|---:|
| DuckDB | 2.4 s | 0.27 s |
| SQL Server | 84.8 s | 1.6 s |
| Postgres | 16.6 s | 6.2 s |

The ETL does the joins and the currency conversion once, not in every query: the same reason you build a data mart
in SQL Server. **Model first, engine second.** ([store results](results/bench-store-scale5-laptop.md))

### 3. Both engines fail the same way when they cannot estimate a join

**Postgres.** The first version of the store SQL built the daily exchange rates in a `MATERIALIZED` CTE.
- A CTE has no statistics — like a table variable in SQL Server.
- Postgres estimated 60,670 rows and got 12 million, then chose a serial nested loop.
- Turning the CTE into a real table made revenue per month **2.4× faster**.

**SQL Server.** The same join on `CAST(placed_at AS date)` had no statistics on the expression.
- It picked four nested-loop joins: **84.8 s**.
- `UPDATE STATISTICS … WITH FULLSCAN` made it *worse*: **101.7 s**.
- A **persisted computed column** with an index let SQL Server estimate the join. The SQL did not change, and it
  took **5.1 s** — three times faster than Postgres.

**The lesson is the same in both engines: when the optimizer cannot estimate a join, it picks the wrong plan.**
([`analytics/mssql-tuning.sql`](../analytics/mssql-tuning.sql))

### 4. One table can serve reports and lookups — in SQL Server, not in DuckDB

A clustered columnstore has no b-tree, so "this customer's last 20 orders" scanned: 61 ms.

A rowstore nonclustered index on the same table brought it **under 1 ms**, and every other question stayed the
same. ([`analytics/mssql-star-index.sql`](../analytics/mssql-star-index.sql))

DuckDB has no equivalent here (6 ms). This is the strongest argument for columnstore when one copy has to serve
both jobs.

Not every columnstore tuning helps. Rebuilding the index ordered by date made six questions slower and none faster,
because the ETL already loads the rows in an order that follows the date.

### 5. Separate the reports from the store, and watch the tail

The load test ran 82 minutes with 5.7 million store operations, 1,200 per second, and every engine on its own cores.

| where the reports ran | checkout p95 | checkout p99 | reports in 20 min |
|---|---:|---:|---:|
| no reports | 22.4 ms | 37.6 ms | — |
| the store's own Postgres | 42.9 ms | **116.5 ms** | 149 |
| SQL Server columnstore | 26.3 ms | 62.7 ms | 1,122 |
| DuckDB service | 25.2 ms | 46.4 ms | **2,108** |

- **A good p95 can hide a bad p99.** With reports on the store's own Postgres, checkout p95 doubles but p99 triples.
- **Past p99.9 the numbers are the client, not the database.** The same 535–650 ms p99.99 appears in every
  scenario: garbage collection and the scheduler of a load generator on a shared machine.

([load test](results/loadtest-scale5-laptop-separate-cores.md))

### 6. The buffer pool is for lookups, not for scans

The cold run emptied each engine's own cache before every timed run: a Postgres restart, `DBCC DROPCLEANBUFFERS`,
and a fresh DuckDB process.
- **Big scans moved less than 10%.** The operating system's cache still held the data, and the work is CPU.
- **Postgres's lookup went from 0.1 ms to 12.3 ms**, 123× slower.
- **DuckDB paid 20–30 ms of start-up per fresh process.** A long-running service avoids that.

([cold run](results/bench-star-scale5-laptop-cold.md))

### 7. pg_duckdb helps least where you need it most

- On the store tables pg_duckdb was 1.5–7× faster than plain Postgres for scans.
- It cannot use Postgres's indexes, so a lookup that takes 0.4 ms in Postgres took **25 minutes**.
- It did not finish 7 of 15 questions within 10 minutes.
- It reads Postgres rows and converts them for a columnar engine: its worst configuration.

On a read replica, with the lookups left to plain Postgres, it is worth trying. It is not worth trying as a drop-in
for the application's database.

### 8. The network decides lookups, the engine decides reports

With a delay added between the app and the database:
- A lookup's time became almost entirely network.
- A report's time barely moved — the engine still decides reports.
- A page that sends 20 small queries pays 20 round trips. Batch them, or keep chatty code next to its database.

([dotnet/README](../dotnet/README.md))

## Rules of thumb

1. **Measure against the right rival.** DuckDB against SQL Server rowstore tells you nothing useful; against
   columnstore it tells you a lot.
2. **Model before engine.** A star schema was worth more than any engine swap here.
3. **Keep lookups on a b-tree.** In Postgres, in SQL Server, or as a rowstore index next to a columnstore.
4. **Reports get their own CPU.** A separate server, or at least separate cores. Never the application's database.
5. **Read the plan before tuning, and before believing the statistics.** Both engines picked nested loops for the
   same join and for the same reason. Fresh statistics did not fix it; giving the optimizer something it can
   estimate did.
6. **Check the answer before the time.** Every engine returned the same rows here, but the checks found a stale
   copy, a question that returned no rows, and a timezone difference along the way.
7. **Look at p99, not p95.** And be suspicious of p99.9 and above: measure the client too.

## Not measured yet

- **How many report users one node can serve** — the concurrency sweep.
- **Disk-bound runs** (a truly cold cache), which need root; planned for the lab.
- **Freshness end to end**: seconds from a checkout until a report sees it, for each architecture.
- **Real separate machines**: the lab setup is ready ([lab/](../lab/README.md), [lab/CONTRACT.md](../lab/CONTRACT.md)).
- **Scale 20** (50M orders, past every engine's memory): running now; results will be added here.

## Where the evidence is

| Topic | File |
|---|---|
| Engines on the star schema | [results/bench-star-scale5-laptop.md](results/bench-star-scale5-laptop.md) |
| Engines on the store tables, SQL Server tuned | [results/bench-store-scale5-laptop.md](results/bench-store-scale5-laptop.md) |
| Cold engine cache | [results/bench-star-scale5-laptop-cold.md](results/bench-star-scale5-laptop-cold.md) |
| Load test, long run, separate cores | [results/loadtest-scale5-laptop-separate-cores.md](results/loadtest-scale5-laptop-separate-cores.md) |
| Load test with SQL Server, first run | [results/loadtest-scale5-laptop-sqlserver.md](results/loadtest-scale5-laptop-sqlserver.md) |
| All of it as charts | [results/charts.html](results/charts.html) |
| Compare page results, Postgres tuning, pg_duckdb, network, ETL | [../dotnet/README.md](../dotnet/README.md) |
