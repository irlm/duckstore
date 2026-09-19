# Cold engine cache: what each engine's own buffer pool is worth

Every timed run starts with the engine's own cache empty: Postgres and pg_duckdb restart (which clears
`shared_buffers`), SQL Server runs `CHECKPOINT; DBCC DROPCLEANBUFFERS; DBCC FREEPROCCACHE`, and DuckDB runs in a
fresh process. Measured 2026-09-19, median of 2 runs, scale 5, star schema:

```bash
make bench-local ARGS="--yes --model star --cold-engine --repeat 2"
```

**This is not a disk-cold run.** The operating system's page cache still held the data, because dropping it needs
root and this laptop has no passwordless sudo. So these numbers say what an engine's *own* buffer pool is worth, not
what the disk costs. The disk-bound version (`--cold`) belongs on the lab machines, where the setup grants that.
Raw rows: [bench-star-scale5-laptop-cold.tsv](bench-star-scale5-laptop-cold.tsv).

## Cold engine cache, median milliseconds

| question | DuckDB | SQL Server | Postgres | pg_duckdb |
|---|---:|---:|---:|---:|
| customer-orders (lookup) | 35.5 | **1.0** | 12.3 | 47.4 |
| unusual-days | **17.0** | 71.0 | 300.0 | 814.6 |
| detail-extract | **40.5** | 146.0 | 481.8 | 987.0 |
| yoy-growth | **218.0** | 1,095.0 | 3,385.8 | 9,695.2 |
| sales-hierarchy | **244.0** | 317.5 | 1,452.3 | 3,940.3 |
| active-customers | **265.5** | 479.5 | 3,784.2 | 4,536.2 |
| clv-buckets | 303.5 | **238.5** | 9,542.8 | 1,583.7 |
| brand-returns | **347.0** | 472.0 | 4,205.3 | 3,028.8 |
| monthly-revenue | **346.0** | 1,488.5 | 6,644.4 | 8,999.5 |
| basket-pairs | **360.0** | 1,465.0 | 8,738.0 | 2,093.3 |
| delivery-percentiles | **500.0** | 12,374.0 | 13,595.6 | 1,889.6 |
| rfm-segments | **534.0** | 762.0 | 17,276.7 | 2,257.9 |
| top-products-per-department | **556.0** | 2,816.5 | 3,425.1 | 4,777.8 |
| cohort-retention | **561.5** | 1,269.0 | 23,072.9 | 5,730.9 |
| category-rollup | **3,203.5** | 79,263.0 | 22,398.3 | 8,321.7 |

## Cold against warm, same engine

| question | DuckDB warm → cold | SQL Server warm → cold | Postgres warm → cold |
|---|---|---|---|
| customer-orders (lookup) | 6 → 35.5 ms (**5.9×**) | 1 → 1 ms (unchanged) | 0.1 → 12.3 ms (**123×**) |
| unusual-days | 11 → 17 ms (1.5×) | 44 → 71 ms (1.6×) | 253 → 300 ms (1.2×) |
| detail-extract | 20 → 40.5 ms (2.0×) | 35 → 146 ms (4.2×) | 76 → 482 ms (6.3×) |
| monthly-revenue | 272 → 346 ms (1.3×) | 1,582 → 1,489 ms (unchanged) | 6,250 → 6,644 ms (1.1×) |
| cohort-retention | 439 → 562 ms (1.3×) | 1,198 → 1,269 ms (1.1×) | 23,670 → 23,073 ms (unchanged) |
| category-rollup | 2,841 → 3,204 ms (1.1×) | 84,280 → 79,263 ms (unchanged) | 22,245 → 22,398 ms (unchanged) |

## What it says

**A big scan barely notices an empty buffer pool.** Revenue per month, cohort retention and the category rollup
change by less than 10% on every engine. Those queries are limited by CPU — scanning and aggregating tens of
millions of rows — and the data comes back from the operating system's cache almost as fast as from the engine's own.
The engine's buffer pool is not what makes an analytics query fast.

**A lookup notices immediately.** Postgres answers "this customer's last 20 orders" in 0.1 ms warm and 12.3 ms cold:
**123× slower**, because the index and heap pages have to be read again. That is the difference between a page in
`shared_buffers` and a page one layer away. SQL Server's lookup stayed at 1 ms — its b-tree pages come back in a
couple of reads, and the plan is the same.

**DuckDB pays a start-up cost.** A fresh process opens the file and rebuilds its own buffers, which is a fixed
20–30 ms — invisible on a 3-second query, 6× on the 6 ms lookup. This is what "DuckDB is a library" means in
practice: no server means no warm process between queries, so short questions pay for the start every time. A
long-running analytics service (as in this project) keeps the process alive and avoids it.

**The ranking does not move.** DuckDB still wins 13 of 15, SQL Server takes the lookup and the customer-value
buckets — the same as warm. A cold cache changes *how much*, not *who*.

## Setup

Same machine and configuration as the [warm star run](bench-star-scale5-laptop.md). SQL Server had the lookup b-tree
of [analytics/mssql-star-index.sql](../../analytics/mssql-star-index.sql) applied, which is why its lookup is 1 ms
here and 61 ms in the first warm table.
