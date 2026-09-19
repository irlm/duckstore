# Reports while the store is busy, with every engine on its own cores

82 minutes, **5.7 million store operations**, four scenarios taking turns. The store always runs on Postgres; only
the reports move — to the same Postgres, to the DuckDB analytics service, or to SQL Server's columnstore.

```bash
make docker-loadtest-separate ARGS="--rate 1200 --duration 300 --warmup 10 --rounds 4 \
  --report-users 2 --scenarios none,postgres,duckdb,sqlserver"
```

Printed tables: [loadtest-scale5-laptop-separate-cores.txt](loadtest-scale5-laptop-separate-cores.txt).

## How it was made trustworthy

- **Cores are split like separate servers** (`docker update --cpuset-cpus`): Postgres 6 threads, the DuckDB service 3,
  SQL Server 4, the web app and load generator 3.
- **The rate is two thirds of capacity.** A probe found the store sustains 1,800 operations/s with a product-page p99
  of 1.9 ms, so 1,200/s measures latency, not saturation. The generator kept the rate in every scenario
  (839.8 + 240.4 + 119.9 = 1,200 per second).
- **Four rounds, taking turns**, not four scenarios back to back, so drift over 82 minutes hits all of them equally.
  Each scenario's four rounds are pooled.
- **Enough samples for the percentile being claimed.** A percentile is an order statistic: p99.99 means nothing
  unless samples sit above it. Per scenario: **1,007,000 product pages** (100 above p99.99), **288,000 order
  histories** (28 above), **144,000 checkouts** (14 above). The tool prints `-` instead of a number when fewer than
  ten samples are above, and reports a **95% confidence interval for p99** from the binomial spread of the rank.

## The store, per scenario (milliseconds)

**store only** — the baseline

| operation | done | p50 | p95 | p99 (95% CI) | p99.9 | p99.99 | max |
|---|---:|---:|---:|---|---:|---:|---:|
| product page | 1,007,735 | 0.2 | 1.1 | 2.2 [2.2 – 2.2] | 116.1 | 535.4 | 1,300 |
| order history | 288,492 | 0.0 | 0.4 | 1.3 [1.2 – 1.3] | 118.5 | 482.0 | 1,094 |
| checkout | 143,829 | 14.8 | 22.4 | 37.6 [36.7 – 38.6] | 209.8 | 586.7 | 1,252 |

**+ reports on Postgres** (the store's own database)

| operation | done | p50 | p95 | p99 (95% CI) | p99.9 | p99.99 | max |
|---|---:|---:|---:|---|---:|---:|---:|
| product page | 1,008,051 | 1.0 | 5.2 | 9.9 [9.7 – 10.0] | 171.3 | 540.0 | 1,578 |
| order history | 288,074 | 0.4 | 4.4 | 7.8 [7.7 – 8.0] | 174.4 | 511.3 | 1,500 |
| checkout | 143,931 | 24.6 | 42.9 | **116.5 [108.0 – 126.6]** | 577.5 | 994.2 | 1,606 |

**+ reports on the DuckDB service**

| operation | done | p50 | p95 | p99 (95% CI) | p99.9 | p99.99 | max |
|---|---:|---:|---:|---|---:|---:|---:|
| product page | 1,007,636 | 0.4 | 1.5 | 3.5 [3.5 – 3.6] | 215.0 | 615.8 | 1,601 |
| order history | 288,494 | 0.0 | 0.8 | 2.3 [2.3 – 2.4] | 198.4 | 599.6 | 1,525 |
| checkout | 143,926 | 16.1 | 25.2 | 46.4 [44.0 – 49.3] | 323.4 | 817.6 | 1,616 |

**+ reports on SQL Server**

| operation | done | p50 | p95 | p99 (95% CI) | p99.9 | p99.99 | max |
|---|---:|---:|---:|---|---:|---:|---:|
| product page | 1,007,418 | 0.4 | 1.4 | 3.4 [3.4 – 3.5] | 204.9 | 565.7 | 1,401 |
| order history | 288,185 | 0.0 | 0.7 | 2.4 [2.3 – 2.4] | 201.8 | 648.3 | 1,312 |
| checkout | 144,453 | 16.4 | 26.3 | 62.7 [57.1 – 68.9] | 317.6 | 603.0 | 1,515 |

## The reports themselves, per 20 minutes of measured time

| reports run on | finished | per second | p50 | p95 |
|---|---:|---:|---:|---:|
| Postgres, store tables | 149 | 0.1 | 10.5 s | 37.2 s |
| SQL Server, star + columnstore | 1,122 | 0.9 | 1.53 s | 5.68 s |
| DuckDB service, star schema | **2,108** | **1.8** | **1.23 s** | **1.85 s** |

## What it says

**Giving the reports their own cores almost removes them from the store's p95.** Product page p95 goes 1.1 ms
(baseline) → 1.4 ms (SQL Server) → 1.5 ms (DuckDB), against **5.2 ms** when the reports run on the store's own
Postgres. Checkout p95: 22.4 → 26.3 → 25.2, against 42.9.

**The tail says more than p95.** Checkout p99 is 37.6 ms alone, 46.4 with DuckDB, 62.7 with SQL Server and
**116.5 with reports on Postgres** — three times the baseline. A p95 that looks fine can hide a p99 that does not,
which is exactly why the tail is worth measuring.

**Beyond p99.9 the database is no longer the story.** Even with nothing else running, a product page whose p99 is
2.2 ms has a p99.9 of 116 ms and a p99.99 of 535 ms — 240× the p99. The same jump appears in every scenario, within
a few percent (535 / 540 / 616 / 566 ms), so it is not caused by the reports. It is the rest of the stack: garbage
collection in the client, connection-pool waits, and the operating system scheduling a load generator that only has
three cores. **A far tail that is the same in every scenario is measuring your client, not your database.**

**DuckDB answers most and disturbs least in the middle; SQL Server sits between.** 2,108 reports against 1,122 and
149. DuckDB's reports are also the most predictable (p95 1.85 s against SQL Server's 5.68 s), because its 3 cores are
its own, while SQL Server's 4 cores have to absorb whole-table scans of a columnstore.

**Reports on the OLTP database are still the worst option, by a wide margin.** 149 reports in 20 minutes, a median
report of 10.5 seconds, and the store's checkout p99 tripled.

## Compared with the first run

The [earlier run](loadtest-scale5-laptop-sqlserver.md) used 100 operations/s, 60 seconds per scenario and shared
cores — 6,000 operations per scenario against 1.4 million here. Its conclusions survive, but its numbers had no
confidence: 600 checkouts cannot say anything about p99, let alone p99.99.

## Limits

- One machine: the "separate servers" are cpusets, not separate hardware. The lab ([lab/](../../lab)) is where that
  gets tested for real.
- The load generator shares the machine with everything it measures, which is what the far tail above shows.
- 1,200 operations/s is a small-to-medium store; the point is the comparison, not the absolute rate.
- Every scenario places about 144,000 orders, whose ids are above the warehouse watermark, so the Compare results do
  not change.
