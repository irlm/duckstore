# Setup 3: Read replica

**Question:** Can reports run on a Postgres replica with pg_duckdb, reading data only seconds old and with no ETL,
without slowing the store?

**Architecture:** the SQL Server version of this is a readable secondary replica.

```
lab-client ──store traffic──► lab-db (Postgres primary) ──streaming replication──► lab-replica (Postgres + pg_duckdb)
           └──reports─────────────────────────────────────────────────────────────►
```

Needs setups 1 and 2. Adds `lab-replica`. Apply [00-every-machine.md](00-every-machine.md) first.

## Machines

| Hostname | Pick the machine with | OS |
|---|---|---|
| `lab-replica` | Close to `lab-db`: the replica holds the same data and runs the reports. Min 32 GB RAM, 1 TB free on NVMe. **Never the `lab-db` machine** | Ubuntu Server 24.04 LTS |

## lab-replica

- **Base:** the Linux server base.
- **Optional pre-pull:** `postgres:18`, `pgduckdb/pgduckdb:18-v1.1.1`.
- **Ports:** 5432 (Postgres).
- **Replication:** Claude sets it up between the containers. The setup agent only opens 5432 between `lab-db` and
  `lab-replica`.

## Extra checks

- `iperf3` between `lab-db` and `lab-replica` in both directions. Replication uses this path.

## Send back

- The row for `lab-replica`.
- The `lab-db` ↔ `lab-replica` iperf3 results.

## What Claude does

**Tests:**

| # | Test | Answers |
|---|---|---|
| 3.1 | Load test: store traffic on `lab-db`. Reports on the replica with plain Postgres, with pg_duckdb, and on the DuckDB service on `lab-mac` | Which report path keeps the store fastest, and which answers fastest? |
| 3.2 | Replica lag, in seconds, during the load test | How fresh are replica reports compared with the incremental ETL (about 11 s plus the schedule)? |
| 3.3 | The 15 questions on the replica with pg_duckdb | Does pg_duckdb work the same on a read-only replica? |
