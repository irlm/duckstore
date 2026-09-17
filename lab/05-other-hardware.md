# Setup 5: Other hardware

**Question:** Do the results from the laptop and `lab-db` hold on different hardware, Apple Silicon included?

**Rule:** machines are different, so compare **ratios** (for example "DuckDB is 20× faster than Postgres"), not seconds.

Needs setup 1 (`lab-client`) and setup 2 (`lab-mac`). Adds `lab-spare`. For more than one spare machine, name them
`lab-spare1`, `lab-spare2` and so on. Apply [00-every-machine.md](00-every-machine.md) first.

## Machines

| Hostname | Pick the machine with | OS |
|---|---|---|
| `lab-spare` | Any x64 machine with a different CPU or disk; being different is the point. Min 16 GB RAM, 500 GB free on SSD. Can be the same machine as `lab-storage` | Ubuntu Server 24.04 LTS |

## lab-spare

- **Base:** the Linux server base.
- **Optional pre-pull:** `postgres:18`, `pgduckdb/pgduckdb:18-v1.1.1`, `mcr.microsoft.com/mssql/server:2025-latest`.
- **Ports:** 5432 (Postgres), 1433 (SQL Server), 5090 (DuckDB service).

## lab-mac (additions)

- `brew install postgresql@18`, not started as a service. Claude starts it only for these tests.
- SQL Server does not run natively on Apple Silicon (only under emulation), so the Mac runs Postgres and DuckDB only.
- **Ports:** 5432 (Postgres).

## Send back

- The row for each `lab-spare`.

## What Claude does

**Tests:**

| # | Test | Answers |
|---|---|---|
| 5.1 | Test 1.1 (15 questions, every engine) on each `lab-spare` | Do the engine ratios hold on other hardware? |
| 5.2 | Postgres and DuckDB service on `lab-mac` vs `lab-db` | x64 vs Apple Silicon |
