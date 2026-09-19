# Store tables (3NF) on four engines, one laptop, scale 5

The same 15 questions as [the star-schema run](bench-star-scale5-laptop.md), but on the normalized tables the
application writes, with the indexes Postgres has and the same ones in SQL Server. Measured 2026-09-19 with
`bench/duckstore-bench-local.sh --model store --repeat 3 --warmup 1 --timeout 600`; raw rows in
[bench-store-scale5-laptop.tsv](bench-store-scale5-laptop.tsv).

Answers were verified first: SQL Server returned exactly the same rows as Postgres for all 15 questions.

## Median milliseconds, lower is better

`–` means the engine did not finish within 600 seconds.

| question | DuckDB | Postgres | SQL Server | pg_duckdb |
|---|---:|---:|---:|---:|
| customer-orders (lookup) | 17 | 0.4 | **0.0** | – |
| detail-extract (23,063 rows) | **33** | 1,051 | 53,388 | 1,702 |
| unusual-days | **316** | 2,197 | 469 | – |
| brand-returns | **452** | 5,277 | 1,670 | 3,974 |
| basket-pairs | **617** | 12,530 | 3,224 | 3,637 |
| clv-buckets | **645** | 12,609 | 55,712 | 3,090 |
| sales-hierarchy | **819** | 3,494 | 56,849 | – |
| active-customers | **845** | 3,337 | 801 | 3,305 |
| rfm-segments | **1,104** | 15,574 | 59,095 | – |
| cohort-retention | **1,966** | 20,492 | 2,314 | 4,907 |
| monthly-revenue | **2,382** | 16,620 | 84,769 | – |
| top-products-per-department | **4,034** | 23,838 | 64,647 | – |
| delivery-percentiles | **6,662** | 13,640 | 16,468 | 29,259 |
| category-rollup | **12,352** | 55,271 | – | 9,505 |
| yoy-growth | **15,273** | 32,164 | 137,730 | – |

## SQL Server picks the wrong join here, and it costs 15×

Seven of the eight questions where SQL Server is far behind join the daily exchange-rate table:

```sql
JOIN store.fx_rates_daily fx ON fx.currency_code = o.currency_code AND fx.day = CAST(o.placed_at AS date)
```

The plan for revenue per month shows **four nested-loop joins** — one index seek per row, across 26.5 million order
lines — where Postgres builds hash tables. Forcing the other join:

| revenue per month, SQL Server | time |
|---|---:|
| the query as written | 84.8 s |
| after `UPDATE STATISTICS … WITH FULLSCAN` | 101.7 s |
| the same query with `OPTION (HASH JOIN)` | **5.6 s** |

So it is not stale statistics and not the hardware: **the optimizer has no statistics for the expression
`CAST(placed_at AS date)`**, estimates the join as tiny, and picks loops. With hash joins SQL Server answers in 5.6 s
— three times faster than Postgres's 16.6 s, which is what the TPC-H lab on real machines also found.

This is the SQL Server twin of a lesson already in this repo: on Postgres the same question was slow until the daily
rate calendar became a real table with statistics, because a MATERIALIZED CTE has none. **Both engines pick a bad
plan when they cannot estimate the join, and both need the same kind of fix.** In SQL Server the production fix is a
persisted computed column with an index:

```sql
ALTER TABLE store.orders ADD placed_date AS CAST(placed_at AS date) PERSISTED;
CREATE NONCLUSTERED INDEX orders_placed_date_idx ON store.orders (placed_date);
```

That is the next experiment, and it will change the numbers above.

## What else the table says

- **DuckDB wins 13 of 15**, and by far more than on the star schema: on 3NF tables the other engines pay for every
  join, while DuckDB scans compressed columns.
- **Both row engines win the lookup.** SQL Server's clustered index answers "this customer's last 20 orders" in
  under a millisecond; DuckDB needs 17 ms because it has no b-tree. That gap is the whole argument for keeping the
  application on a row store.
- **SQL Server beats Postgres where it can use batch mode or a good plan**: active customers 0.8 s vs 3.3 s, cohort
  retention 2.3 s vs 20.5 s, unusual days 0.5 s vs 2.2 s, basket pairs 3.2 s vs 12.5 s.
- **pg_duckdb did not finish 7 of 15** within 600 s on these tables. It reads Postgres rows and converts them for a
  columnar executor, which is its worst configuration — the same caveat the TPC-H lab records.
- **The data model is worth more than the engine.** Compare with the star-schema table: DuckDB goes from 2,382 ms to
  272 ms on revenue per month, SQL Server from 84.8 s to 1.6 s, Postgres from 16.6 s to 6.2 s. Reshaping the data
  helps every engine more than swapping one engine for another.

## Setup

Same machine and method as the star run. SQL Server was capped at 5 GB of memory partway through this run (it had
been unlimited), Postgres had `shared_buffers` 3 GB plus the OS page cache, DuckDB 6 GB.
