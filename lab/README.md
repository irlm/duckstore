# Lab

Tests on real machines: SQL Server, Postgres and DuckDB on separate servers, over a real network. The laptop results
are in [dotnet/README.md](../dotnet/README.md). The lab checks whether they hold and tests what one laptop can't.

## How it works

1. Give the setup agent [00-every-machine.md](00-every-machine.md) and **one** setup file.
2. The agent installs the OS and software only, runs the checks and fills in the send-back table.
3. Send the table to Claude. Claude writes `lab/hosts.env`, copies the app, loads the data, runs the tests and saves
   the results in `docs/results/lab/`.
4. The next setup keeps the machines that are already set up and adds new ones.

## Setups

| # | File | Architecture | Question | New machines | Reuses |
|---|---|---|---|---|---|
| 1 | [01-sql-server.md](01-sql-server.md) | Warehouse server: SQL Server columnstore | How does SQL Server compare with Postgres and DuckDB on the same hardware? Does Windows vs Linux matter? | lab-db, lab-sqlwin, lab-client | none |
| 2 | [02-analytics-server.md](02-analytics-server.md) | DuckDB service on its own server | Does a separate analytics server keep the store fast? How many report users can one node serve? | lab-mac | lab-db, lab-client |
| 3 | [03-read-replica.md](03-read-replica.md) | pg_duckdb on a Postgres read replica | Can reports read data only seconds old, with no ETL, without slowing the store? | lab-replica | lab-db, lab-client, lab-mac |
| 4 | [04-data-lake.md](04-data-lake.md) | Parquet and DuckLake on object storage | Can DuckDB processes on several machines share one copy of the data? | lab-storage | lab-db, lab-client, lab-mac, lab-replica |
| 5 | [05-other-hardware.md](05-other-hardware.md) | Setup 1 again, on other machines | Do the results hold on different hardware, Apple Silicon included? | lab-spare | lab-client, lab-mac |

## Machines

| Hostname | Role | Setups |
|---|---|---|
| `lab-db` | Database server (Linux): Postgres, pg_duckdb, SQL Server on Linux, DuckDB service | 1, 2, 3, 4 |
| `lab-sqlwin` | SQL Server on Windows Server | 1 |
| `lab-client` | Web app and test runner | all |
| `lab-mac` | Mac mini: DuckDB service without Docker | 2, 3, 4, 5 |
| `lab-replica` | Postgres read replica with pg_duckdb | 3, 4 |
| `lab-storage` | S3-compatible object storage | 4 |
| `lab-spare` | Different hardware | 5 |

**Sharing rules.** A machine can have more than one role; send the same IP for each role.
- `lab-client` never shares a machine with a database or storage server used in the same test. The test load would
  steal that server's CPU.
- `lab-replica` is never the `lab-db` machine.
- The best `lab-sqlwin` is the `lab-db` machine booting Windows from a second disk, so both OSes share the same hardware.
- `lab-storage` and `lab-spare` can be the same machine.

**With 4 PCs and the Mac mini, all five setups fit:**

| Machine | Roles |
|---|---|
| Most RAM and fastest NVMe | `lab-db` + `lab-sqlwin` (dual boot) |
| The next biggest | `lab-replica` |
| Fastest network and disk of the rest | `lab-storage` + `lab-spare` |
| The smallest | `lab-client` |
| Mac mini | `lab-mac` |

Setups 1 and 2 need only 2 PCs and the Mac.
