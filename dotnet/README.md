# DuckStore for .NET

The same store as the Go project, in C#, as **two ASP.NET Core services** that look like one application:

| Project | What it does | Talks to |
|---|---|---|
| `DuckStore.Web` | Blazor + MudBlazor UI: **Products** (CRUD), **Reports**, **ETL** and **Compare** pages; product REST API | Postgres (EF Core, Npgsql) and the analytics service (HTTP) |
| `DuckStore.Analytics` | Owns the **DuckDB** warehouse file: ETL, reports API, analytics API | DuckDB (inside the process); Postgres only during the ETL |
| `DuckStore.Contracts` | The records both services exchange as JSON | |

For the user it is one application. Underneath, each part runs where it works best, and the **Compare** page
runs 15 questions six ways (three engines × two data models) so you can see what each part is worth.

```mermaid
flowchart LR
    browser["Browser"] <-->|SignalR| web
    subgraph web["web container: DuckStore.Web"]
        pages["Blazor pages"]
        runner["ComparisonRunner"]
    end
    subgraph analytics["analytics container: DuckStore.Analytics"]
        api["/api/reports<br/>/api/analytics<br/>/api/etl"]
        duck["DuckDB.NET<br/>(in-process)"]
        etl["WarehouseBuilder<br/>runs etl/*.sql"]
        api --> duck
    end
    pages -->|"EF Core: small transactions"| PG[("postgres container<br/>store schema")]
    runner -->|"Postgres protocol"| PG
    runner -->|"HTTP + JSON"| api
    duck -->|READ_ONLY| DW[("warehouse volume<br/>/data/warehouse.duckdb")]
    PG -->|"ATTACH (TYPE postgres)"| etl
    etl -->|"builds a new file, then swaps"| DW
```

## Run it

### Everything in Docker

You only need Docker. From the `duckstore` folder:

```bash
make docker-up      # build and start postgres, analytics and web
make docker-seed    # load the fake store data (SCALE=5 by default) and build the warehouse
make docker-star    # optional: copy the star schema into Postgres, for the Compare page's star schema approaches
make docker-pgduckdb   # optional: enable pg_duckdb, DuckDB's engine inside Postgres
```

Then open:

- http://127.0.0.1:5085/compare: the Compare page (http://127.0.0.1:5085 for the rest of the app)
- http://127.0.0.1:5085/scalar and http://127.0.0.1:5090/scalar: API references of the two services

Without `make`:

```bash
docker compose up -d --build
docker compose run --rm seed -scale 5                # the seed image is a small Go binary; no Go install needed
docker compose run --rm --no-deps analytics etl      # or press "Run ETL now" on the ETL page
docker compose run --rm --no-deps analytics star-to-postgres
```

On the test laptop, scale 5 (12.5M orders, 26.5M order lines) took about **3 minutes** to seed (about 10 GB
in Postgres) and **68 seconds** to build the warehouse (a 2.6 GB DuckDB file). Copying the star schema into
Postgres took **3 minutes** (DuckDB moved 42M rows in about a minute; keys, indexes and VACUUM ANALYZE took the
rest). `make docker-seed SCALE=1` is five times smaller.

Other commands:

```bash
make docker-compare   # the Compare questions in the terminal (see below)
make docker-etl       # rebuild the warehouse; the running service switches to the new file
make docker-etl-incremental   # load only what changed since the last ETL (about 11 s instead of 60 s)
make docker-logs      # follow the analytics and web logs
make docker-indexes   # add the covering indexes of the Postgres tuning experiment (make docker-indexes-drop removes them)
make docker-loadtest  # store traffic alone, with reports on Postgres, with reports on the DuckDB service
make docker-loadtest-separate   # the same, with Postgres and the analytics service on their own CPU cores
make down             # stop the containers (data stays in the volumes)
make reset            # stop and delete all data
```

**Memory.** All three containers share one machine. On a 28 GB laptop with a desktop open, Postgres gets
`shared_buffers=3GB` and DuckDB `memory_limit=6GB` (`Warehouse__MemoryLimit`), both in `docker-compose.yml`.
With larger values the seed's index build ran the machine low on memory. `memory_limit` is DuckDB's
version of SQL Server's "max server memory": without it, DuckDB may use 80% of the RAM.

### Locally, for development

Requires the .NET 10 SDK. Postgres still runs in Docker.

```bash
make up                  # only Postgres in Docker
make seed                # fake data with Go, or: docker compose run --rm seed -scale 1
make dotnet-etl          # build data/warehouse.duckdb with the C# ETL
make dotnet-analytics    # http://127.0.0.1:5090
make dotnet-run          # http://127.0.0.1:5085 (in another terminal)
make dotnet-test         # tests against the real Postgres and DuckDB
```

The local apps use the same ports as the containers: stop those first with `docker compose stop web analytics`.
In Development, EF Core prints every SQL statement it runs to the terminal.

## The Compare page

Each question can run **eight ways**: four engines on two data models.

| | Store tables: normalized, as the application writes them | Star schema: facts and dimensions built by the ETL |
|---|---|---|
| **Postgres** | `store.*`, sent by the web app over the Postgres protocol | `dw.*`, a copy made by `make docker-star` |
| **SQL Server** | `store.*` in SQL Server, rowstore, with the same keys and indexes Postgres has | `dw.*` in SQL Server, with a **clustered columnstore index** on the facts |
| **pg_duckdb** | the same `store.*` tables in the same Postgres, executed by DuckDB inside Postgres | the same `dw.*` copy, executed by DuckDB inside Postgres |
| **DuckDB** | `raw.*`, the ETL's copy of the store tables, through the analytics service | `dw.*`, through the analytics service |

- On the **store tables**, Postgres, pg_duckdb and DuckDB run the **same SQL text** (see
  [analytics/README.md](../analytics/README.md)), so the difference is the **engine**. SQL Server runs its own T-SQL,
  because the dialect differs — a file that does not exist is reported as missing, never replaced by another engine's SQL.
- On the **same engine**, store tables vs star schema shows what the **data model** is worth: the ETL did the joins
  and the currency conversion once, instead of in every query.
- Postgres runs go from the web app straight to Postgres. DuckDB runs go over HTTP to the analytics service and come
  back as JSON, so their times include the service boundary.

All eight answers must be equal. The store SQL only counts orders up to the warehouse **watermark** (the last order id
the ETL copied), and the star schema copy in Postgres remembers the watermark it was made from: after a new ETL, the
page asks you to run `make docker-star` again. The page compares the results row by row, with a tolerance of one
cent for money.

**Measuring.** One run can be unlucky: a cold cache, or another program busy for a moment. Choose a warm-up run and
3-7 timed runs, and the page shows the **median** run (half of the runs were faster, half slower), plus the fastest
and the slowest. The approaches take turns (1, 2, 3, 4, 1, 2, ...), so a slow moment on the machine hits all of them.

**Plans.** Every panel has a **Plan** button: the estimated plan (`EXPLAIN`, the query does not run) or the actual
plan (`EXPLAIN ANALYZE`, it runs), with a short guide to reading that engine's plan in SQL Server terms.

**Where the time goes.** Each run is split into phases:

| Phase | Postgres and SQL Server | DuckDB |
|---|---|---|
| **Database** | From sending the query until the first row arrives. For a small result this is almost everything. | Measured inside the service: executing until the first row, then reading the other rows. |
| **Network** | Reading the other rows from the connection and decoding them. | The HTTP time seen by the web app minus the service's own phases (sent in a `Server-Timing` header). It includes the network, Kestrel and HttpClient. |
| **JSON** | None: both wire protocols are binary. | Serializing in the service plus parsing in the web app. |

**SQL Server** is started on demand, because it wants memory next to Postgres and DuckDB:

```bash
make docker-mssql        # SQL Server 2025 Developer, password written to .env, port 51433
make docker-mssql-load   # the warehouse copied in: rowstore store tables, columnstore star schema
make docker-mssql-down   # stop it and get the memory back
```

Its plans come from `SET SHOWPLAN_TEXT` (estimated) and `SET STATISTICS PROFILE` (actual), the pair that matches
`EXPLAIN` and `EXPLAIN ANALYZE`.

### Results at scale 5

Median of 5 runs after 1 warm-up run, measured with `compare --runs 5 --warmup 1` inside the web container on an
AMD Ryzen 7 5825U (8 cores / 16 threads), 28 GB RAM, NVMe SSD, all containers on the same laptop. Data: scale 5
= 1.5M customers, 150k products, 12.5M orders, 26.5M order lines. Postgres 18 with the settings in
`docker-compose.yml` (up to 8 parallel workers per query) and no extra indexes, DuckDB 1.5.5 with 16 threads and a
6 GB memory limit. **All 15 questions gave the same result in all four approaches.** The store-table columns were
measured after the store SQL was tuned (see [Tuning Postgres](#tuning-postgres-statistics-and-indexes)); the
star-schema columns come from the run before, which that change does not touch. Every run is in
[docs/results/compare-scale5.csv](../docs/results/compare-scale5.csv).

| Question | Postgres · store | DuckDB · store | Postgres · star | DuckDB · star | Engine, store tables | Engine, star schema | Model, Postgres | Model, DuckDB |
|---|---:|---:|---:|---:|---|---|---|---|
| Revenue per month | 15.0 s | 2.30 s | 6.62 s | 281 ms | DuckDB 6.5× | DuckDB 24× | star 2.3× | star 8.2× |
| Revenue by category with subtotals | 48.7 s | 11.5 s | 20.7 s | 2.51 s | DuckDB 4.2× | DuckDB 8.3× | star 2.4× | star 4.6× |
| Active customers per month | 2.99 s | 849 ms | 3.05 s | 204 ms | DuckDB 3.5× | DuckDB 15× | about equal | star 4.2× |
| Cohort retention | 19.2 s | 1.87 s | 21.7 s | 442 ms | DuckDB 10× | DuckDB 49× | store 1.1× | star 4.2× |
| RFM customer segments | 14.4 s | 1.08 s | 12.6 s | 426 ms | DuckDB 13× | DuckDB 30× | star 1.1× | star 2.5× |
| Top 3 products per department | 21.4 s | 3.99 s | 2.90 s | 444 ms | DuckDB 5.4× | DuckDB 6.6× | star 7.4× | star 9.0× |
| Products bought together | 11.2 s | 547 ms | 7.44 s | 237 ms | DuckDB 21× | DuckDB 31× | star 1.5× | star 2.3× |
| Return rate by brand | 4.79 s | 417 ms | 2.93 s | 227 ms | DuckDB 11× | DuckDB 13× | star 1.6× | star 1.8× |
| Delivery time percentiles | 13.0 s | 6.53 s | 12.8 s | 320 ms | DuckDB 2.0× | DuckDB 40× | about equal | star 20× |
| Revenue by sales leader | 3.17 s | 849 ms | 990 ms | 145 ms | DuckDB 3.7× | DuckDB 6.8× | star 3.2× | star 5.9× |
| Unusual days | 2.03 s | 305 ms | 183 ms | 7.7 ms | DuckDB 6.7× | DuckDB 24× | star 11× | star 40× |
| Year-over-year growth by department | 29.4 s | 14.9 s | 2.82 s | 159 ms | DuckDB 2.0× | DuckDB 18× | star 10× | star 94× |
| Customer lifetime value buckets | 11.5 s | 612 ms | 8.63 s | 194 ms | DuckDB 19× | DuckDB 45× | star 1.3× | star 3.2× |
| All order lines of one day (42,581 rows) | 134 ms | 190 ms | **109 ms** | 239 ms | Postgres 1.4× | Postgres 2.2× | star 1.2× | store 1.3× |
| One customer's latest orders (lookup) | 0.6 ms | 12.8 ms | **0.4 ms** | 3.5 ms | Postgres 21× | Postgres 9× | star 1.5× | star 3.6× |

What the numbers say:

- **Both the engine and the data model matter, and they multiply.** For the 13 analytics questions, the slowest
  way (Postgres on the store tables) and the fastest way (DuckDB on the star schema) are 15-265× apart. The engine
  alone, with the same SQL on the same tables, gave 2-21×. The star schema alone gave up to 11× on Postgres and up
  to 94× on DuckDB.
- **The star schema helps DuckDB on every analytics question, Postgres only on some.** Postgres gained where the
  star schema removed work that dominates in a row store: currency conversion for every order line (revenue per
  month 2.3×, year-over-year 10×), a recursive walk of the org chart (sales leaders 3.2×), grouping by an expression
  (`placed_at::date`, unusual days 11×), and a date filter an index can serve (top products 7.4×). Where the query
  must read every row of a table anyway (active customers, cohort retention, RFM, delivery), Postgres gained almost
  nothing: a row store reads whole rows, and `dw.fact_orders` has 29 columns where `store.orders` has 13. DuckDB
  reads only the 2-3 columns it needs.
- **A good model can beat a faster engine.** Postgres on the star schema beat DuckDB on the store tables for top
  products (2.90 s vs 3.99 s), year-over-year growth (2.82 s vs 14.9 s) and unusual days (183 ms vs 305 ms). The
  plan of top products shows why: Postgres reads only the last 12 months through the `order_date` index, and the
  window function stops after 3 rows per department (`Run Condition` in the plan).
- **The same SQL is not always good SQL for both engines.** On the store tables DuckDB was only 2× faster for
  delivery percentiles. Its plan shows where the time goes: `extract(epoch FROM delivered_at - shipped_at)`.
  Subtracting two `timestamptz` values uses time-zone calendar arithmetic for every row in DuckDB, while in Postgres
  it is a cheap integer subtraction. On the scale-1 shipments, that expression took 1.10 s in DuckDB, and
  `extract(epoch FROM delivered_at) - extract(epoch FROM shipped_at)` took 19 ms for the same sum. The star schema
  avoided the problem because the ETL already chose each order's last parcel.
- **Large results and lookups: Postgres.** For all order lines of one day, most of DuckDB's time is network and JSON
  (4.2 MB); Postgres sends the rows in its binary protocol. For one customer's orders, a B-tree index answers in
  under 1 ms; DuckDB scans a column (the fact table is sorted by date, so zone maps cannot skip blocks for a
  customer).
- **The service boundary is cheap for small results.** Network plus JSON took 1-5 ms per analytics question, less
  than 1% of the DuckDB time for most of them.
- **Repeat small measurements.** For the analytics questions, the fastest and the slowest of the 5 runs were less than
  10% apart in 43 of 52 cases (27% at most). For the large result and the lookup they were up to 75% apart: one run
  of those would be a guess.

Limits of this measurement: one machine, so the containers share the same cores and memory; one user at a time;
Postgres without partitioning or columnar storage.

### Tuning Postgres: statistics and indexes

A comparison is only fair if Postgres is tuned the way a DBA would tune it in production. So before the results
above, Postgres got the usual treatment: read the plans, fix what they show, measure again. Every step below is the
median of 5 runs of Postgres · store tables, unless it says EXPLAIN ANALYZE.

**1. Read the plans.** `EXPLAIN (ANALYZE, BUFFERS)` of the 13 analytics queries (the Plan button on the page) showed
three kinds of problems:

| What the plans showed | Questions |
|---|---|
| **A wrong row estimate.** The first version of the store SQL built one exchange rate per currency per day in a `MATERIALIZED` CTE. A CTE has no statistics, like a table variable in SQL Server, so Postgres estimated that joining 12M orders to it returns **60,670** rows. It returned **12,124,454**. With the small estimate Postgres chose a serial nested loop: 12M index lookups into `order_items`, no parallel workers. | revenue per month, by category, year-over-year, top products, sales leaders, RFM, lifetime value |
| **Work spilled to disk.** `count(DISTINCT ...)` sorted 25.7M rows on disk (1.2 GB); per-customer hash tables spilled 200-350 MB. | revenue per month, cohort, RFM, lifetime value, delivery |
| **Reading the table where an index could do.** Order lines looked up in the loops above, the last parcel of each order, the first order of each customer. | several |

**2. Statistics: a real table instead of the CTE.** The daily rates became a table,
[`store.fx_rates_daily`](../internal/pg/schema/03_fx_rates_daily.sql), built by the seed (a production system would
rebuild it when new rates arrive). The store SQL joins it, on both engines. Postgres now estimates the join
correctly and uses parallel hash joins:

| Question | CTE | Table | |
|---|---:|---:|---|
| Revenue per month | 36.6 s | 15.0 s | 2.4× faster |
| Revenue by sales leader | 8.16 s | 3.17 s | 2.6× faster |
| Year-over-year growth | 49.7 s | 29.4 s | 1.7× faster |
| Revenue by category | 63.0 s | 48.7 s | 1.3× faster |
| Top 3 products per department | 25.9 s | 21.4 s | 1.2× faster |
| Customer lifetime value buckets | 12.4 s | 11.5 s | 1.1× faster |

What did **not** help, tested with EXPLAIN ANALYZE on revenue per month: `SET work_mem = '512MB'` (the sort needs more
than 1.2 GB, so it still spilled), an expression index on `(placed_at AT TIME ZONE 'UTC')::date` (it gave statistics
to the orders side of the join, but the problem was the CTE side), and `SET enable_nestloop = off` (a diagnosis tool,
not a fix: 36 s, still serial because the estimate was still wrong).

**3. Covering indexes.** [analytics/postgres-indexes.sql](../analytics/postgres-indexes.sql) adds three indexes that
match what the plans read (`make docker-indexes`, `make docker-indexes-drop`):

| Index | For |
|---|---|
| `order_items (order_id) INCLUDE (line_no, product_id, quantity, unit_price, discount)` | order lines read per order |
| `orders (customer_id, placed_at) INCLUDE (id, status, currency_code, total)` | per-customer questions |
| `shipments (order_id, delivered_at DESC NULLS FIRST, shipped_at DESC NULLS FIRST) INCLUDE (carrier)` | the last parcel per order, in index order |

They took 18 s to build and use **2.9 GB**, 63% of the size of the three tables (4.6 GB). Measured again
([docs/results/compare-postgres-indexes-scale5.csv](../docs/results/compare-postgres-indexes-scale5.csv)):

| Question | Without | With the 3 indexes | Change | Index the plan uses |
|---|---:|---:|---:|---|
| Revenue by sales leader | 3.17 s | 1.91 s | −40% | order lines + customer |
| Customer lifetime value buckets | 11.5 s | 9.69 s | −16% | customer |
| RFM customer segments | 14.4 s | 12.6 s | −12% | customer |
| Delivery time percentiles | 13.0 s | 11.6 s | −11% | shipments |
| Cohort retention | 19.2 s | 17.8 s | −7% | customer |
| Active customers per month | 2.99 s | 2.88 s | −4% | customer |
| Revenue by category with subtotals | 48.7 s | 47.0 s | −3% | order lines |
| Year-over-year growth | 29.4 s | 28.6 s | −2% | order lines |
| All order lines of one day | 134 ms | 131 ms | −2% | order lines |
| Revenue per month | 15.0 s | 15.3 s | +2% | customer |
| Products bought together | 11.2 s | 11.9 s | +6% | customer |
| Top 3 products per department | 21.4 s | 22.8 s | +7% | customer |
| Return rate by brand | 4.79 s | 5.17 s | +8% | customer |
| Unusual days | 2.03 s | 2.55 s | +25% | customer |

Changes below about 5% are within the run-to-run noise.

The price on writes: copying 100,000 orders with their 211,000 lines and 97,000 shipments in one transaction
([analytics/postgres-write-cost.sql](../analytics/postgres-write-cost.sql), median of 4 runs, rolled back):

| Insert | Without | With the 3 indexes |
|---|---:|---:|
| 100,000 orders | 2.15 s | 2.38 s (+11%) |
| 211,214 order lines | 3.85 s | 3.99 s (+4%) |
| 97,035 shipments | 1.08 s | 1.23 s (+14%) |

The order lines index grows at its right end (new order ids are the largest), so it is cheap to maintain; the
customer index gets inserts all over the tree.

What tuning teaches:

- **Statistics before indexes.** One table with statistics gave up to 2.6×. The best index gave 1.7× on one question
  and slowed down others.
- **An index changes plans you did not look at.** Postgres used the customer index in 11 of 15 questions, as a
  narrower copy of `orders`, not only for per-customer work. In a first attempt, while the CTE was still there, that
  made revenue per month 65% slower in EXPLAIN ANALYZE: the index returns orders sorted by customer, so the nested
  loop looked up order lines in random order instead of in id order. Always measure every query, not only the one
  you tune.
- **Index-only scans need VACUUM.** The rolled-back write test left 400,000 dead rows; until VACUUM ran, an
  index-only scan read the table 400,021 times ("Heap Fetches" in the plan) for unusual days.
- **Tuning closes part of the gap, not all of it.** After tuning, DuckDB on the same tables is still about 2-21× faster
  for the analytics questions, and on the star schema 15-265× faster than Postgres on the store tables. Postgres still
  wins lookups and large results. The remaining gap is the storage layout: rows vs columns.

### pg_duckdb: DuckDB's engine inside Postgres

[pg_duckdb](https://github.com/duckdb/pg_duckdb) is a Postgres extension that runs a query with DuckDB's engine while
the data stays in Postgres. It splits the gap between Postgres and the analytics service into two parts:

- **Execution:** Postgres vs pg_duckdb, same server, same tables, same SQL. Only the executor changes: row by row
  vs vectors on many threads.
- **Storage:** pg_duckdb vs the DuckDB service, same engine. Only the storage changes: Postgres pages read and
  converted row by row vs DuckDB's own compressed column file.

Setup: the Postgres image is the official `postgres:18` with the pg_duckdb 1.1 files added
([docker/postgres.Dockerfile](../docker/postgres.Dockerfile)), `shared_preload_libraries=pg_duckdb` in
`docker-compose.yml`, and `make docker-pgduckdb` runs `CREATE EXTENSION pg_duckdb`. The Compare page's pg_duckdb
connections set `duckdb.force_execution = true` and read each Postgres table with 8 threads and 8 workers instead
of the default 2 and 2 ([analytics/postgres-pg_duckdb.sql](../analytics/postgres-pg_duckdb.sql)); with the defaults,
unusual days took 1.46 s instead of 0.91 s. Median of 5 runs, scale 5, all 15 results equal to the other approaches.

**Store tables:**

| Question | Postgres | pg_duckdb | DuckDB service | Execution: Postgres → pg_duckdb | Storage: pg_duckdb → DuckDB file |
|---|---:|---:|---:|---|---|
| Revenue per month | 15.0 s | 5.26 s | 2.30 s | 2.9× faster | 2.3× faster |
| Revenue by category with subtotals | 48.7 s | 7.27 s | 11.5 s | 6.7× faster | 1.6× slower |
| Active customers per month | 2.99 s | 1.64 s | 849 ms | 1.8× faster | 1.9× faster |
| Cohort retention | 19.2 s | 3.92 s | 1.87 s | 4.9× faster | 2.1× faster |
| RFM customer segments | 14.4 s | 2.89 s | 1.08 s | 5.0× faster | 2.7× faster |
| Top 3 products per department | 21.4 s | 9.85 s | 3.99 s | 2.2× faster | 2.5× faster |
| Products bought together | 11.2 s | 2.35 s | 547 ms | 4.8× faster | 4.3× faster |
| Return rate by brand | 4.79 s | 3.12 s | 417 ms | 1.5× faster | 7.5× faster |
| Delivery time percentiles | 13.0 s | 8.10 s | 6.53 s | 1.6× faster | 1.2× faster |
| Revenue by sales leader | 3.17 s | 3.62 s | 849 ms | 1.1× slower | 4.3× faster |
| Unusual days | 2.03 s | 918 ms | 305 ms | 2.2× faster | 3.0× faster |
| Year-over-year growth | 29.4 s | 6.46 s | 14.9 s | 4.5× faster | 2.3× slower |
| Customer lifetime value buckets | 11.5 s | 1.60 s | 612 ms | 7.2× faster | 2.6× faster |
| All order lines of one day | **134 ms** | 7.11 s | 190 ms | **53× slower** | 37× faster |
| One customer's latest orders | **0.6 ms** | **about 25 minutes** (one run) | 12.8 ms | | |

**Star schema:**

| Question | Postgres | pg_duckdb | DuckDB service | Execution: Postgres → pg_duckdb | Storage: pg_duckdb → DuckDB file |
|---|---:|---:|---:|---|---|
| Revenue per month | 6.62 s | 3.68 s | 281 ms | 1.8× faster | 13× faster |
| Revenue by category with subtotals | 20.7 s | 6.32 s | 2.51 s | 3.3× faster | 2.5× faster |
| Active customers per month | 3.05 s | 1.94 s | 204 ms | 1.6× faster | 9.5× faster |
| Cohort retention | 21.7 s | 4.53 s | 442 ms | 4.8× faster | 10× faster |
| RFM customer segments | 12.6 s | 2.20 s | 426 ms | 5.7× faster | 5.2× faster |
| Top 3 products per department | 2.90 s | 2.57 s | 444 ms | 1.1× faster | 5.8× faster |
| Products bought together | 7.44 s | 1.54 s | 237 ms | 4.8× faster | 6.5× faster |
| Return rate by brand | 2.93 s | 2.35 s | 227 ms | 1.3× faster | 10× faster |
| Delivery time percentiles | 12.8 s | 1.51 s | 320 ms | 8.5× faster | 4.7× faster |
| Revenue by sales leader | 990 ms | 2.12 s | 145 ms | 2.1× slower | 15× faster |
| Unusual days | 183 ms | 661 ms | 7.7 ms | 3.6× slower | 86× faster |
| Year-over-year growth | 2.82 s | 4.50 s | 159 ms | 1.6× slower | 28× faster |
| Customer lifetime value buckets | 8.63 s | 1.37 s | 194 ms | 6.3× faster | 7.1× faster |
| All order lines of one day | **109 ms** | 882 ms | 239 ms | 8.1× slower | 3.7× faster |
| One customer's latest orders | **0.4 ms** | 9.9 ms | 3.5 ms | about 25× slower | 2.8× faster |

What the numbers say:

- **The executor alone is worth a lot.** On the same Postgres tables, pg_duckdb was 1.5-7.2× faster than Postgres for
  12 of the 13 analytics questions, with no ETL and no second system: the data is always current.
- **But it has no indexes, and its optimizer is built for analytics.** Every question Postgres answers with an index
  got slower. The large result took 7.1 s instead of 134 ms: the filter for one day compares `placed_at` with a
  subquery, so it is not pushed into the Postgres scan and DuckDB reads all 12.5M orders. The lookup of one customer's
  20 orders ran for about 25 minutes instead of 0.6 ms: DuckDB turned its per-order subqueries (a `LATERAL` exchange
  rate and a `count(*)` of the lines) into joins over the whole tables, with a window function over an estimated
  265 million rows, and applied the customer filter at the end. On the star schema, where Postgres has date indexes,
  pg_duckdb was slower for 5 of the 15 questions.
- **Storage is the other big part.** With the same engine, DuckDB's own file was faster than reading Postgres tables
  for every star schema question (2.5-86×) and for 11 of 13 analytics questions on the store tables (1.2-7.5×).
  Postgres rows have to be read page by page and converted into DuckDB vectors; the DuckDB file stores each column
  compressed, with min/max values per block.
- **Two results we could not explain.** On the store tables, pg_duckdb beat the analytics service for year-over-year
  growth (6.5 s vs 14.9 s) and revenue by category (7.3 s vs 11.5 s). Both plans convert 25.7M order lines to USD
  with the same DECIMAL → DOUBLE → DECIMAL expression; in the service's plan that projection alone took 11.9 s. The Plan
  button shows both plans if you want to dig in.
- **Where it fits.** pg_duckdb gives Postgres analytics that are several times faster on current data, without an
  ETL. It runs on the Postgres server, so reports still use the CPU and memory the store needs (the load test showed
  what that costs; `duckdb.max_memory` is 4 GB per connection by default), and it must never run the application's
  lookups. Set `duckdb.force_execution` only on the connections or role that run reports, never for the whole server.

### SQL Server: the engine most teams already have

`make docker-mssql` starts SQL Server 2025 Developer, `make docker-mssql-load` copies the warehouse into it (rowstore
store tables with the same keys and indexes Postgres has, star schema with a clustered columnstore index). Every
question has its own T-SQL in `analytics/<id>/<model>.mssql.sql`, and all 30 files were checked against Postgres row
by row before any timing.

Measured at scale 5 on this laptop — full tables in
[docs/results/bench-star-scale5-laptop.md](../docs/results/bench-star-scale5-laptop.md) and
[docs/results/bench-store-scale5-laptop.md](../docs/results/bench-store-scale5-laptop.md):

- **Columnstore is DuckDB's real competitor, not row storage.** On the star schema SQL Server beat the same schema in
  Postgres on 14 of 15 questions, and DuckDB's lead shrank from 10–90× to 2–5×.
- **A columnstore is the wrong storage for a lookup** — "this customer's last 20 orders" took 61 ms, against 0.1 ms
  in Postgres. `make docker-mssql-star-index` adds a b-tree next to the columnstore and it drops to under 1 ms, with
  no effect on the reports. One table, both jobs.
- **`make docker-mssql-star-ordered`** rebuilds the columnstore sorted by date. It made six questions slower and none
  faster here, because the ETL already loads the facts in an order that follows the date. Kept as a documented
  negative result.
- **On the store tables SQL Server chose nested loops** for the join to the daily exchange-rate table, because it has
  no statistics for `CAST(placed_at AS date)`: revenue per month took 84.8 s, and `UPDATE STATISTICS … WITH FULLSCAN`
  made it worse (101.7 s). `make docker-mssql-tuning` adds a **persisted computed column** for that date, with an
  index and statistics; the plan becomes one hash join and the same query takes 5.1 s — three times faster than
  Postgres. The large extract went from 53.4 s to 0.085 s.
  This is the same lesson Postgres taught here earlier with the exchange-rate CTE: **an engine that cannot estimate a
  join picks a bad plan, whatever its name is.**

`make docker-mssql-tuning-drop`, `make docker-mssql-star-index-drop` and `make docker-mssql-star-ordered-drop` undo
each experiment, so both sides can be measured.

### Network latency

Everything above ran with direct connections inside one Docker network, where a round trip costs well under 1 ms.
In production the web app, Postgres and the analytics service are often on different machines. The **Network**
setting (CLI: `--latency direct,0,1,5,25`) sends both paths through [Toxiproxy](https://github.com/Shopify/toxiproxy),
a container that forwards the connections and adds a delay in each direction: 1 ms is like another server in the same
data center, 5 ms another data center in the same city, 25 ms another region.

Median of 5 runs after 1 warm-up run, star schema on both engines, scale 5. Every run is in
[docs/results/compare-latency-scale5.csv](../docs/results/compare-latency-scale5.csv).

| Question | Approach | Direct | Proxy, no delay | +1 ms each way | +5 ms each way | +25 ms each way |
|---|---|---:|---:|---:|---:|---:|
| One customer's latest orders (lookup) | Postgres · star | 0.3 ms | 0.3 ms | 2.6 ms | 10.9 ms | 51.0 ms |
| | DuckDB · star | 4.9 ms | 5.4 ms | 6.7 ms | 14.7 ms | 55.6 ms |
| Unusual days | Postgres · star | 190 ms | 190 ms | 190 ms | 197 ms | 231 ms |
| | DuckDB · star | 8.1 ms | 7.6 ms | 9.8 ms | 18.5 ms | 59.0 ms |
| Revenue by sales leader | Postgres · star | 833 ms | 833 ms | 817 ms | 823 ms | 852 ms |
| | DuckDB · star | 149 ms | 148 ms | 147 ms | 153 ms | 191 ms |
| Revenue per month | Postgres · star | 6.06 s | 5.91 s | 5.88 s | 5.79 s | 5.92 s |
| | DuckDB · star | 272 ms | 280 ms | 275 ms | 277 ms | 321 ms |
| All order lines of one day (4 MB) | Postgres · star | 100 ms | 103 ms | 101 ms | 110 ms | 146 ms |
| | DuckDB · star | 179 ms | 174 ms | 183 ms | 185 ms | 232 ms |

What the numbers say:

- **One question costs one round trip.** A delay of N ms in each direction adds about 2 × N ms to every question,
  on both engines and whatever the query does: +25 ms each way added 41-58 ms to everything above, except the two
  slowest Postgres queries (0.8 s and 6 s), where it disappears in the run-to-run noise. The proxy itself adds no
  measurable time.
- **For lookups, the network decides.** Direct, Postgres answers the lookup 16× faster than the DuckDB service.
  At +5 ms each way it is 10.9 ms vs 14.7 ms, and at +25 ms the two are almost equal: the database work is a
  small part of the time.
- **For analytics, the engine decides.** +25 ms each way is 1% of Postgres' 6 seconds for revenue per month, and
  15% of DuckDB's 280 ms. DuckDB is still 18× faster.
- **Round trips multiply.** These questions are one query each. A page that sends 20 separate queries pays 20
  round trips: at +5 ms each way that is about 200 ms before any database work. Batch the queries, or keep chatty
  code close to its database.
- **Measure time until the first row, not only the total.** The page subtracts a round trip measured with
  `SELECT 1` from Postgres' "until the first row" time. Without that, network delay would look like slow database work.

Limits: Toxiproxy adds a delay but does not make the link slower or lose packets. TCP between the web app and the
proxy is still local, so a 4 MB response does not pay the extra round trips a real long-distance connection needs
while TCP grows its sending window. Over a real 25 ms link the large result would be slower than shown here.

### Load test: do reports slow down the store?

The Compare page runs one question at a time. In production, customers use the store while other people run reports.
The **Load test** page (CLI: `dotnet DuckStore.Web.dll loadtest`, or `make docker-loadtest`) measures that:

- **Store traffic:** 100 operations per second for 60 seconds, after 5 seconds of warm-up. 70% product pages (three
  reads by key in one round trip), 20% order histories (a lookup), 10% checkouts: one transaction that locks the stock
  rows, updates them and inserts the order, its lines, a payment and a shipment.
- **Open loop:** operations start on schedule even when Postgres is slow, and latency counts from the scheduled start,
  so waiting for a free connection counts too. A test that sends the next request only when the last one finished
  would hide a slow database, because it would simply send fewer requests.
- **Report users:** 2 people, each running 5 analytics questions one after the other (brand returns, active customers,
  sales leaders, unusual days, revenue per month), on Postgres (store tables), through the DuckDB service (star schema),
  or on SQL Server (star schema with a clustered columnstore).
- **p95 and p99:** 95% and 99% of the operations were faster than this. Customers remember the slow page, so the tail
  matters more than the average.

It ran twice. First, all containers shared the laptop's 16 threads (`make docker-loadtest`). Then each server got its
own cores, like separate machines (`make docker-loadtest-separate`, which uses `docker update --cpuset-cpus`): Postgres 4
cores, the analytics service 3 cores, the web app with the load generator 1 core. Every operation of both runs is in
[docs/results/loadtest-shared-cpu.csv](../docs/results/loadtest-shared-cpu.csv) and
[docs/results/loadtest-separate-cores.csv](../docs/results/loadtest-separate-cores.csv).

**All containers share the CPU** (p95 / p99 in ms):

| Store operation | Store only | + reports on Postgres | + reports on DuckDB service |
|---|---:|---:|---:|
| Product page | 1.4 / 2.1 | 4.9 / 14.1 | 4.4 / 6.6 |
| Order history | 0.7 / 1.5 | 1.2 / 3.5 | 3.2 / 5.9 |
| Checkout | 18.5 / 23.5 | **35.0 / 88.4** | 24.6 / 30.3 |
| Reports finished in 60 s | | 15 (median 3.9 s) | 366 (median 359 ms) |

**Each server on its own cores** (p95 / p99 in ms):

| Store operation | Store only | + reports on Postgres | + reports on DuckDB service |
|---|---:|---:|---:|
| Product page | 1.8 / 2.3 | 5.2 / 10.0 | **1.5 / 2.5** |
| Order history | 1.4 / 8.8 | 2.9 / 5.5 | **0.8 / 1.1** |
| Checkout | 18.5 / 24.3 | **27.5 / 75.4** | **19.4 / 23.9** |
| Reports finished in 60 s | | 15 (median 4.6 s) | 182 (median 707 ms) |

What the numbers say:

- **Reports on the store's database hurt the store's slowest moments.** With reports on Postgres, the checkout p99 went
  from 24 ms to 75-88 ms, 3-4× worse, and the worst 5-second window of checkouts had a p95 of 69-79 ms instead of about
  20 ms. The median barely moved (9 ms to 11-12 ms): an average would hide the problem. The reports and the store
  compete for the same CPU cores, and each report query can use up to 8 parallel workers.
- **On its own cores, the DuckDB service did not affect the store.** Checkout p95 19.4 ms vs 18.5 ms without reports,
  product page 1.5 ms vs 1.8 ms. This is the production setup: analytics on another server.
- **On the same machine, DuckDB competes for the CPU too.** DuckDB uses every core for each query, so with shared cores
  the store also got slower during DuckDB reports (checkout p95 24.6 ms, order history p95 3.2 ms), although the tail
  suffered much less than with reports on Postgres (checkout p99 30 ms vs 88 ms). Running the analytics service next to
  the database saves a server, not CPU.
- **The reports themselves finish.** In 60 seconds the Postgres report users finished 15 reports (revenue per month
  alone takes 15 s). The DuckDB users finished 182 reports on 3 cores and 366 on the shared 16 threads.

**With SQL Server as a third report target** (all containers sharing the CPU, 2026-09-19, full write-up in
[docs/results/loadtest-scale5-laptop-sqlserver.md](../docs/results/loadtest-scale5-laptop-sqlserver.md)):

| Store operation, p95 | Store only | + reports on Postgres | + reports on DuckDB service | + reports on SQL Server |
|---|---:|---:|---:|---:|
| Product page | 2.2 | 5.4 | 5.5 | **3.4** |
| Order history | 1.9 | 2.8 | 3.2 | **2.4** |
| Checkout | 19.8 | 42.5 | 31.3 | **28.6** |
| Reports finished in 60 s | | 14 (p50 4.3 s) | **311 (p50 419 ms)** | 124 (p50 757 ms) |

- **Any engine of its own beats reporting on the OLTP database.** SQL Server answered 8.9× more reports than Postgres,
  DuckDB 22× more, and in both cases the store stayed faster than with reports on Postgres.
- **SQL Server disturbed the store least, DuckDB answered fastest.** DuckDB takes every core it can for one query,
  which is why its reports finish first and why the store notices it slightly more on a shared machine.

Limits: one 60-second run per scenario; the load generator runs on the same machine; 100 operations per second is a
small store; every scenario places about 600 orders, whose ids are above the warehouse watermark, so the Compare results
do not change. Analyze the raw data with DuckDB:

```sql
SELECT scenario, operation,
       quantile_cont(latency_ms, 0.95) AS p95, quantile_cont(latency_ms, 0.99) AS p99
FROM 'loadtest-separate-cores.csv'
WHERE ok AND second >= 5          -- after the warm-up
GROUP BY ALL
ORDER BY ALL;
```

### The same measurement in the terminal

```bash
make docker-compare
# or with options:
docker compose exec web dotnet DuckStore.Web.dll compare --runs 5 --warmup 1 --csv /tmp/runs.csv monthly-revenue customer-orders
docker compose exec web dotnet DuckStore.Web.dll compare --approaches duckdb-store,duckdb-star    # only some approaches
```

`--csv` writes one row per run (warm-up runs too). DuckDB reads the file directly, so you can analyze the
benchmark with the engine you are learning:

```sql
SELECT question, approach,
       median(total_ms) AS median_ms, min(total_ms) AS fastest, max(total_ms) AS slowest
FROM 'runs.csv'
WHERE NOT warmup
GROUP BY ALL
ORDER BY question, approach;
```

## Incremental ETL

A full ETL copies all 12.5M orders again and rebuilds every table: about 60 seconds at scale 5, plus a heavy read on
Postgres, and the warehouse is only as fresh as the last run. An **incremental** ETL loads only what changed since the
last run. Start it with the **Incremental ETL** button on the ETL page, `make docker-etl-incremental`,
`POST /api/etl/runs?mode=incremental`, `dotnet DuckStore.Analytics.dll etl --incremental`, or on a schedule with
`Etl:IncrementalMinutes`. A common setup: incremental every few minutes, full once a night.

How it works ([etl/incremental/incremental.sql](../etl/incremental/incremental.sql)):

1. **Snapshot.** The current warehouse file is copied with a **reflink**: on btrfs (and XFS, APFS) both files share their
   blocks until one of them changes, so the 2.6 GB file "copies" in 15-50 ms instead of 4.3 s. The update runs on the
   copy while the reports keep reading the old file, and the copy is renamed over it at the end, exactly like a full ETL.
2. **What changed.** `dw.etl_info` remembers the last run's watermark (highest order id) and when it read Postgres. One
   query finds the orders to load: ids above the old watermark (new orders), orders whose `updated_at` is after the last
   run minus 5 minutes (shipped, delivered, cancelled), and orders with returns newer than the last loaded return.
3. **Delta.** Only those orders' rows (orders, lines, payments, shipments, returns) come from Postgres, through
   `postgres_query`, which runs the SQL in Postgres with its indexes.
4. **Replace.** The changed orders are deleted and inserted again in `raw.*` and in the facts. The fact rows come from the
   **full ETL's own SQL**, run in an in-memory database that holds only the delta, so there is one copy of the business
   logic. `customer_order_number` (1 = the customer's first order) needs the customer's whole history, so it is
   recomputed for the affected customers.
5. **Everything else.** Small tables and dimensions are copied and rebuilt; `dim_product` gets new price versions as SCD
   Type 2 with stable keys; reviews, inventory and the marts are rebuilt. There is no Parquet export (the full ETL does it).

Measured at scale 5 ([etl/incremental/demo-changes.sql](../etl/incremental/demo-changes.sql) makes the changes):

| Run | What changed in Postgres | Orders loaded | Time |
|---|---|---:|---:|
| Full ETL | | 12,511,207 | 58.6-62.2 s |
| Incremental | nothing (first version, which still rewrote all 650,085 product rows) | 0 | 12.3 s |
| Incremental | 300 returns, 100 new prices, 20 renamed products | 300 | 10.4 s |
| Incremental | 1,000 orders shipped, 500 delivered, 200 cancelled, 300 returns, 100 new prices, 20 renames | 2,000 | 12.0 s |
| Incremental | 292 new orders from the load test (and the orders above again, inside the 5-minute overlap) | 1,992 | 11.1 s |
| Incremental | the same again: the overlap loads the same orders, with the same result | 1,992 | 11.0 s |

The changed orders themselves took well under a second (finding them 0.3 s, the delta and replacing them about 0.3 s).
The rest is fixed cost: rebuilding `dim_customer` from 1.5M customers (2.6 s), the "bought together" mart (2.4 s) and copying
customers, addresses and reviews (about 2.5 s). Those could become incremental too, with an `updated_at` on customers.

**Proof, not hope.** After every round, a full ETL wrote a second file and `compare-warehouses` compared all 37 tables
row by row (`EXCEPT ALL`), plus the product version every sale points to:

```bash
docker compose exec analytics dotnet DuckStore.Analytics.dll etl --target /data/full-check.duckdb
docker compose exec analytics dotnet DuckStore.Analytics.dll compare-warehouses /data/warehouse.duckdb /data/full-check.duckdb
```

The final result: "The two warehouses hold the same data", 25.7M sales rows included. The first comparison was not
clean, and that was the point of it: 87 rows of `dim_product` kept old product names. The condition
`... OR d.color IS DISTINCT FROM p.attributes ->> '$.color'` never matched, because in DuckDB `->>` binds more loosely
than `OR`: the whole `OR` chain became the left side of `->>`. Parentheses fixed it. No error message would have shown
this bug; only comparing with a full build did.

What an incremental ETL needs, and where it breaks:

- **Stable surrogate keys.** The full ETL numbers product versions with `row_number()`, which is fine when every fact is
  rebuilt. An incremental ETL keeps old sales, so old keys must never change: new versions get keys after the highest one.
- **A reliable change marker.** Status changes are found through `orders.updated_at`, which the store sets. A change made
  without it (a manual `UPDATE`) or a hard `DELETE` is not seen until the next full ETL. Databases can report every change
  themselves (logical replication in Postgres, change data capture in SQL Server); that is the next level.
- **Idempotent steps.** Delete + insert of whole orders, with an overlap window: loading an order twice gives the same
  result, so a failed or repeated run is harmless.
- **An occasional full ETL.** The incremental file grew from 2,586 MB to 2,785 MB and stayed there: DuckDB reuses the
  space of deleted rows but does not shrink the file. A full ETL writes a compact file (2,588 MB) and exports Parquet.

## The code

```
src/DuckStore.Contracts/
  Analytics.cs                  the 15 Compare questions, request and result records
  Reports.cs, Etl.cs            report and ETL records
  Cells.cs                      one value format for both engines, and the "same result" rule
src/DuckStore.Analytics/        the analytics service
  Program.cs                    services, endpoints, `etl` and `install-extensions` commands
  Warehouse/DuckDbWarehouse.cs  opens the DuckDB file read-only, reopens it after an ETL
  Warehouse/ReportService.cs    report SQL (the same SQL as the Go dashboard)
  Analytics/AnalyticsRunner.cs  runs a question's SQL on DuckDB (store or star model) and times it
  Etl/StarToPostgres.cs         copies the star schema into Postgres (etl/postgres-star/)
  Etl/WarehouseComparer.cs      compare-warehouses: proves two warehouse files hold the same data
  Etl/WarehouseBuilder.cs       the ETL: lock, new file, ATTACH postgres, run etl/*.sql, Parquet, swap
  Etl/EtlService.cs             runs it in the background for the ETL page, the API and the schedule
  Api/                          minimal API endpoints and error mapping (ProblemDetails)
src/DuckStore.Web/              the web app
  Data/StoreDbContext.cs        EF Core mapping of the existing Postgres tables (no migrations)
  Services/ProductService.cs    CRUD + business rules (price versions, delete guard)
  Services/AnalyticsApiClient.cs typed HttpClient for the analytics service
  Services/Comparison.cs        runs a question both ways, repeats, splits the time into phases
  Services/CompareCommand.cs    the terminal version of the Compare page
  Services/NetworkLab.cs        slows the connections down through Toxiproxy
  Services/LoadTest.cs          store traffic (open loop) with report users; LoadTestCommand.cs for the terminal
  Components/Pages/             Home, Products, Reports, Etl, Compare, LoadTest
tests/DuckStore.Web.Tests/      product API tests over the real Postgres
tests/DuckStore.Analytics.Tests/ ETL, lock, report and SQL splitter tests
../analytics/                   one folder per Compare question: store.sql, star.sql or star.<engine>.sql
../etl/                         the ETL SQL, shared with the Go version; etl/incremental/ for incremental loads
../docker/                      Dockerfiles for seed, analytics and web
```

Design decisions worth reading in the code:

| Decision | Why |
|---|---|
| DuckDB lives only in the analytics service | One process owns the file, so there is one place for the ETL, the lock and the reopen logic. The web app has no DuckDB dependency and can be scaled or deployed on its own. The price is a network hop plus JSON on every call (measured on the Compare page). |
| A shared `Contracts` project | Both services compile against the same records, so a changed field breaks the build, not production. |
| `Server-Timing` header on analytics responses | The service reports its own execute, read and serialize times. The web app subtracts them from what it measured, so what is left is network and HTTP overhead. |
| DuckDB copies the star schema into Postgres (`etl/postgres-star/`) | DuckDB's postgres extension writes with Postgres' binary COPY, so a few lines of SQL move 42M rows in about a minute. Postgres then adds the keys and indexes a DBA would add, and `VACUUM ANALYZE` like the seed does, so the Postgres star schema is not handicapped. |
| The warehouse reader sets `TimeZone = 'UTC'` | Casting a `timestamptz` to a date cuts the day at midnight of the session's time zone. Outside Docker the laptop's zone gave different days than the ETL and Postgres; all 15 questions differed until this was set. |
| The store SQL casts rounded USD amounts to `numeric(14,2)` | DuckDB divides a DECIMAL by a DECIMAL into a DOUBLE. Summing doubles made one customer's lifetime total 999.99999... instead of 1,000.00, which moved them to another bucket. Postgres `numeric` is exact; the cast makes both engines exact, as the ETL already does. |
| One folder of SQL per Compare question ([analytics/README.md](../analytics/README.md)) | `store.sql` runs on both engines (DuckDB reads the ETL's `raw.*` copy), so the store tables isolate the engine. The star schema has `star.sql`, or one file per engine when the dialects differ. All versions return the same columns, so the results can be compared cell by cell. |
| Postgres SQL is bounded by the watermark | Postgres has orders that the warehouse does not have yet. Without the bound the answers would differ for the wrong reason. |
| EF Core for CRUD, Dapper for reports | EF Core shines at loading and saving entities. Reports are SQL written by hand for DuckDB (PIVOT, QUALIFY, ASOF), so a thin mapper is better. |
| `IDbContextFactory`, one DbContext per operation | In Blazor Server a scoped service lives as long as the browser tab; one DbContext must not serve overlapping operations. |
| A price change = close the current `product_prices` row + insert a new one, in one `SaveChanges` | Keeps the history the warehouse turns into a Type 2 slowly changing dimension. |
| Delete refuses sold products (409) | Order history must survive. Deactivate instead. |
| One in-memory DuckDB with the file attached `READ_ONLY`, a `Duplicate()` connection per query | Opening the file costs ~13 ms, a duplicate ~0.4 ms. Read-only lets the Go app and the ETL work at the same time. |
| Reopen when the file changes | The ETL writes a new file and renames it. The next query opens the new file; queries on the old one finish normally. |
| `Warehouse:MemoryLimit` and `Warehouse:Threads` | DuckDB takes 80% of the RAM and every core by default. Limit them when it shares the machine with Postgres. |
| Casts in report SQL (`::BIGINT`, `::DECIMAL`) | DuckDB `sum()` of integers returns HUGEINT (`BigInteger` in .NET), and division returns DOUBLE. |
| ETL SQL embedded from the top-level `etl/` folder | One copy of the SQL for every runner. The C# code only handles what SQL cannot: the lock, the files, the swap. |
| ETL writes `warehouse.duckdb.building`, then `File.Move(..., overwrite: true)` | DuckDB allows one writer per file. The reports never see a half-built warehouse, and a failed ETL leaves the old one in place. |
| Lock file `etl.lock` opened with `FileShare.None` | On Linux/macOS that is an exclusive `flock()`, the same lock the Go ETL takes, so two ETLs can never run at once (tested). |
| A failed startup or scheduled ETL is logged, not thrown | Otherwise an empty database at the first `docker compose up` would stop the analytics container. |
| The Docker image installs the `postgres` extension at build time | The container needs no internet access to run the ETL. |
| Incremental ETL on a reflink copy, then the same swap | The reports never see a half-updated warehouse and a failed run leaves the old file untouched, as with a full ETL, while the copy costs milliseconds on a copy-on-write file system. |
| `-- @full:` lines in incremental.sql run steps of the full ETL | The fact SQL (currency conversion, SCD2 lookup, last shipment...) exists once. The incremental file only adds what is incremental: finding changes, deleting and inserting, stable keys. |
| The Postgres image copies pg_duckdb into the official `postgres:18` | The pg_duckdb project's own image is Postgres 18.1 on Debian 12; the data volume was created by the official image on Debian 13. A different C library can sort text differently and break text indexes, so only the extension files are copied. |
| `RequiresAspNetWebAssets` in the web project | `blazor.web.js` comes from a NuGet package that the SDK adds only when it sees `.razor` files at restore time. The Dockerfile restores from the `.csproj` alone (for layer caching), so without this the pages load but nothing is interactive. |

## One backend or two?

**One is enough for most applications.** DuckDB is a library, not a server: the query runs inside DuckDB's C++
engine whichever process calls it, so moving it to another service does not make the query faster. Before
the split, this app ran CRUD, reports and the ETL in one process.

This project splits into two services **on purpose**, to measure what a service boundary costs. The Compare
page shows the price: a few milliseconds of HTTP per call, plus JSON time that grows with the number of rows.
Next to a 300 ms analytics query that is noise; next to a 1 ms lookup by key it is most of the time.

Split DuckDB into its own service when:

- **The ETL or heavy reports slow down the web requests.** Measured in the load test: reports on the store's Postgres
  made the checkout p99 3-4× worse; the DuckDB service on its own cores left the store unchanged. On the same cores
  DuckDB slows the store down too, because it uses every core for each query.
- **Several applications** need the same warehouse, not only this web app.
- **It scales, deploys or belongs to a team differently** from the web app.

Keep it in one process when one application uses it, the results are small, and one deployment is simpler.

Rules that apply in every case:

1. **Only one process writes the DuckDB file**, and that is the ETL, writing a new file and swapping it.
   Everything else opens it `READ_ONLY`.
2. **Never write orders into DuckDB** from the web app. Writes go to Postgres; DuckDB gets them from the next ETL.
3. **Keep lookups by key on Postgres.** An index finds one customer's orders in about 1 ms; the service path is
   slower before the query even starts.
4. For **"right now" numbers**, ask Postgres (as the Home page does for orders after the watermark), or combine
   the warehouse with a small live Postgres query.
