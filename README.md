# duckstore

A learning project that answers one question with real code and real numbers:
**when should you use Postgres, and when should you use DuckDB?**

It is an online store built twice over the same data:

- **The store** runs on **Postgres** (OLTP): browse products, fill a cart, check out, ship, cancel, return.
  Every click is a small transaction on a normalized schema with 24 related tables.
- **The analytics** run on **DuckDB** (OLAP): an ETL copies Postgres into a star schema in a DuckDB file,
  and a dashboard asks big questions about millions of rows.

Every page shows the exact SQL it ran, with timings. An **engine race** page runs the same questions on
both engines, and a **SQL console** with 12 lessons is written for SQL Server developers.

```mermaid
flowchart LR
    UI["Store UI<br/>(browse, cart, checkout)"] -->|small transactions| PG[("Postgres 18<br/>store schema, 3NF<br/>~14M rows")]
    PG -->|"ETL: ATTACH postgres,<br/>build new file, swap"| DW[("DuckDB file<br/>raw + star schema")]
    DW --> PQ[/"Parquet files<br/>partitioned by year/month"/]
    DW --> AN["Analytics UI<br/>(dashboard, race, console)"]
    PG -.->|"live queries through<br/>ATTACH (hybrid)"| AN
```

There are two implementations over the same databases and the same SQL files:

- **Go**: one binary with the store, the analytics dashboard, the engine race and the SQL console.
  DuckDB runs **inside** the Go process (it is a library, not a server).
- **.NET**: a Blazor app plus a separate DuckDB **analytics service**, all in Docker, with a **Compare** page
  that runs 15 questions on Postgres and through the service and shows where the time goes.

## Results on a laptop

One run on an AMD Ryzen 7 5825U (8 cores / 16 threads), 28 GB RAM, NVMe SSD, Postgres 18 in Docker with
parallel query on, DuckDB 1.5.5. Dataset: 300k customers, 30k products, 2.5M orders, 5.3M order lines.
Reads show the median of the timed runs. Run `make bench` or open the engine race page to measure your machine.

### Analytics (scan, join, aggregate)

| Question | Postgres (store tables) | DuckDB (same tables) | DuckDB (star schema) |
|---|---:|---:|---:|
| Revenue per month in USD, all history | 24.6 s | 1.0 s | **65 ms** |
| Revenue by category tree with ROLLUP | 29.9 s | 1.4 s | **454 ms** |
| Distinct active customers per month | 1.59 s | 180 ms | **48 ms** |
| Cohort retention | 3.55 s | 1.19 s | **89 ms** |
| Return rate by brand (5-table join) | 1.04 s | 105 ms | **53 ms** |

The middle column uses the **same normalized tables** as Postgres, copied by the ETL: that gap is the
**engine** (column store, vectorized, all cores). The last column adds the **data model**: joins and
currency conversion were done once, in the ETL.

### The application's queries

| Question | Postgres | DuckDB (same tables) | DuckDB (star schema) |
|---|---:|---:|---:|
| One order with its lines (point lookup) | **0.23 ms** | 2.6 ms | 2.0 ms |
| A customer's 10 latest orders | **0.13 ms** | 1.6 ms | 1.7 ms |
| 5,000 single-row INSERTs in one transaction | **291 ms** (17,162/s) | 1.19 s (4,192/s) | |
| 5,000 single-row UPDATEs by key in one transaction | **371 ms** (13,489/s) | 1.16 s (4,323/s) | |
| 300 INSERTs, each committed on its own | 2.02 s (148/s) | 2.14 s (140/s) | |
| 8 concurrent writers on the same 16 rows (1,200 updates) | **2.5 s, 0 failed** | 7.4 s, **160 failed** | |
| Bulk load 5M rows with one INSERT ... SELECT | 3.50 s | **361 ms** | |

What the numbers say:

- **Lookups by key**: Postgres uses a B-tree and touches a few pages. DuckDB has no B-tree on these
  tables and scans (with zone maps). Both are fast; Postgres is about 10× faster, which matters at thousands of requests per second.
- **Small writes**: in one transaction, Postgres does 3-4× more statements per second. With a commit per
  statement, both engines mostly wait for the disk to confirm the write-ahead log (fsync), so they look alike.
- **Concurrency**: Postgres locks the row and the second writer waits; DuckDB uses optimistic concurrency,
  so 13% of the updates **failed** with a conflict and would need a retry. Across processes DuckDB is
  stricter: only one process can write to a database file.
- **Big writes**: DuckDB loads 5M rows about 10× faster. DuckDB is not "bad at writes"; it is bad at
  *many small concurrent* writes.

### So, which one?

| Use **Postgres** when | Use **DuckDB** when |
|---|---|
| It is the application's source of truth | You analyze a copy of the data |
| Many users write at the same time | One process writes, in batches (ETL) |
| Queries touch a few rows by key | Queries scan and aggregate millions of rows |
| You need users, roles, replication, high availability | You want no server at all (it runs inside your app) |
| | You want to query Parquet/CSV/JSON files or a data lake directly |

Most real systems use **both**, like this project: Postgres runs the store, DuckDB answers the analytics,
and DuckDB can even read Postgres live when a question needs fresh data.

## The .NET version in Docker: Postgres direct vs a DuckDB service

[dotnet/](dotnet/) runs as three containers:

| Container | What runs there |
|---|---|
| `postgres` | the store database |
| `analytics` | an ASP.NET Core service that owns the DuckDB file: the ETL (same SQL files as Go, in [etl/](etl/)), reports and analytics over HTTP |
| `web` | the Blazor + MudBlazor app: product CRUD with EF Core, reports, the ETL page, and the **Compare** page |

```bash
make docker-up      # build and start the three containers (needs only Docker)
make docker-seed    # 12.5M orders (SCALE=5) and the warehouse; about 4 minutes
# open http://127.0.0.1:5085/compare
```

The Compare page runs the same question two ways, checks that the results are equal, repeats the runs and
shows the median, split into database, network and JSON time. Selected results at scale 5:

| Question | Postgres direct | DuckDB service | Faster |
|---|---:|---:|---|
| Year-over-year growth by department | 49.2 s | 130 ms | DuckDB, 378× |
| Revenue per month in USD | 36.5 s | 277 ms | DuckDB, 132× |
| Active customers per month | 2.96 s | 209 ms | DuckDB, 14× |
| All order lines of one day (42,581 rows) | 264 ms | 283 ms | about equal: 223 ms of it is JSON |
| One customer's latest orders (lookup by key) | **0.9 ms** | 5.2 ms | Postgres, 5.7× |

Medians of 5 runs on the same laptop, 12.5M orders, 26.5M order lines. The HTTP hop between the containers
costs 1-3 ms per question.

Read [dotnet/README.md](dotnet/README.md) for all 15 questions, how the time is measured, and the answer to
**"one backend or two?"**

## Quick start (Go version)

Requirements: Docker, Go 1.26+, a C compiler (DuckDB is linked with CGO), and internet access the first
time (DuckDB downloads its `postgres` extension once).

```bash
make up        # start Postgres 18 in Docker on port 55432
make seed      # create the schema and load ~14M rows (about 30 s)
make etl       # build the DuckDB warehouse from Postgres (about 13 s)
make run       # http://localhost:8080  (store)  and  /analytics
```

Smaller data for a quick try: `make seed SCALE=0.1`.

Other commands:

```bash
make bench                                                  # the engine race in the terminal
./bin/duckstore bench -only order-lookup                    # one scenario
./bin/duckstore sql "SUMMARIZE dw.fact_orders"              # query the warehouse (DuckDB)
./bin/duckstore sql -engine postgres "SELECT count(*) FROM orders"
./bin/duckstore sql -f lessons/02-window-functions.sql      # run a lesson in the terminal
make test                                                   # unit tests
make reset                                                  # delete Postgres data and the warehouse
```

If `make up` says `permission denied ... docker.sock`, add yourself to the docker group
(`sudo usermod -aG docker $USER`) and log in again.

## A tour

1. **Store home** (`/`): departments with product counts from a recursive CTE, best sellers of the last
   7 days computed live in Postgres, and "popular in 90 days" chosen by DuckDB with names and prices from Postgres (a hybrid page).
2. **A product page**: breadcrumb (recursive CTE walking up), stock per warehouse, rating summary with
   `FILTER`, price history (a temporal table), and "frequently bought together" pre-computed by DuckDB.
   Change the price to create a new price version.
3. **Cart and checkout**: checkout is one transaction of about 12 statements: lock the cart, lock stock
   rows in a fixed order (no deadlocks), pick a warehouse per line, take the stock with one `UPDATE ... FROM unnest(...)`,
   insert the order, lines, payment and shipments, empty the cart. The statements are listed on the next page.
4. **The order page**: ship, deliver, cancel (puts stock back) and return single units.
5. **Account** (`/account`): referral chains and the account manager's chain of command, both recursive.
   Switch to a customer in another country to see another currency.
6. **Analytics** (`/analytics`): KPIs, monthly revenue, a dynamic `PIVOT`, a cohort retention heatmap,
   delivery percentiles, a hierarchy rollup, unusual days, and the number of orders placed since the last ETL,
   read live from Postgres by DuckDB.
7. **Engine race** (`/analytics/race`): press Run on any question.
8. **SQL console** (`/analytics/sql`): load a lesson, run it, tick EXPLAIN ANALYZE.
9. **ETL** (`/analytics/etl`): run the ETL and watch the steps. Place an order first, then find it in `dw.fact_sales`.

## Learn

| Guide | What it covers |
|---|---|
| [DuckDB for SQL Server developers](docs/01-duckdb-for-sql-server-developers.md) | Types, syntax and concepts mapped from T-SQL |
| [OLTP vs OLAP, and how this project shares data](docs/02-architecture.md) | Row vs column storage, DuckDB concurrency rules, the ETL design (watermark, build and swap), hybrid queries |
| [The data model](docs/03-data-model.md) | The 24 store tables and the star schema, with diagrams |
| [lessons/](lessons/) | 12 SQL lessons, loadable in the SQL console |

The SQL itself is meant to be read:

- [internal/pg/schema/](internal/pg/schema/): the Postgres schema, keys and indexes
- [etl/](etl/): the whole ETL in four SQL files, run by both the Go and the .NET version
- [internal/bench/scenarios.go](internal/bench/scenarios.go): every race question on both engines
- [internal/web/store.go](internal/web/store.go) and [checkout.go](internal/web/checkout.go): the OLTP queries and transactions
- [internal/web/analytics.go](internal/web/analytics.go): the dashboard questions

## Project layout

```
analytics/              the 15 Compare questions: one folder each, SQL per data model and engine
cmd/duckstore/          the binary: seed, etl, serve, bench, sql
cmd/seed/               seed only, without DuckDB (used by the Docker seed image)
docker/                 Dockerfiles: seed, analytics, web
dotnet/                 the ASP.NET Core version: web app + analytics service
etl/                    the ETL SQL, run by both the Go and the .NET version
internal/config/        settings from environment variables
internal/pg/            Postgres connection and schema files
internal/seed/          fake data generator (deterministic, bulk COPY)
internal/warehouse/     ETL (DuckDB) and the read-only warehouse reader
internal/query/         run SQL on either engine, same result shape
internal/bench/         engine race scenarios
internal/sqlsplit/      split SQL scripts into statements
internal/web/           HTTP handlers, templates, charts (no JS framework)
lessons/                SQL lessons (embedded in the binary)
docs/                   guides
```

## Settings

| Variable | Default |
|---|---|
| `DUCKSTORE_PG_URL` | `postgres://store:store@127.0.0.1:55432/store?sslmode=disable` |
| `DUCKSTORE_DATA_DIR` | `data` (warehouse file, Parquet, scratch files) |
| `DUCKSTORE_LISTEN` | `127.0.0.1:8080` |

The web app is a local learning tool: the SQL console runs any SQL you type, and there is no login.
Keep it on localhost.
