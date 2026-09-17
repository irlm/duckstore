# Setup 1: SQL Server

**Question:** How do SQL Server rowstore and columnstore compare with Postgres, pg_duckdb and DuckDB on the same
hardware? Does Windows vs Linux matter?

**Architecture:** a warehouse server. This is SQL Server with columnstore indexes, the choice closest to what SQL Server
teams already run.

```
lab-client ──► lab-db:     Postgres, pg_duckdb, DuckDB service, SQL Server on Linux (one at a time)
           └─► lab-sqlwin: SQL Server on Windows
```

Apply [00-every-machine.md](00-every-machine.md) to every machine first.

## Machines

| Hostname | Pick the machine with | OS |
|---|---|---|
| `lab-db` | Most RAM and the fastest NVMe. Min 32 GB RAM (64 GB+ is better), 8+ cores, **1 TB free** on NVMe | Ubuntu Server 24.04 LTS |
| `lab-sqlwin` | Best: the `lab-db` machine itself, with Windows on a second disk (dual boot). Otherwise: x64 as close to `lab-db` as possible, min 32 GB RAM, 500 GB free on NVMe | Windows Server 2025 Standard, Desktop Experience (evaluation is OK) |
| `lab-client` | Any x64 with 8+ threads and 16 GB RAM | Ubuntu Server 24.04 LTS |

`lab-db` needs 1 TB for three reasons:
- Scale 20 is 4× the laptop data.
- Every engine keeps its own copy.
- One pg_duckdb query wrote 179 GB of temp files on the laptop.

## lab-db

- **Base:** the Linux server base.
- **Optional pre-pull:** `postgres:18`, `pgduckdb/pgduckdb:18-v1.1.1`, `mcr.microsoft.com/mssql/server:2025-latest`.
- **Ports:** 5432 (Postgres), 1433 (SQL Server), 5090 (DuckDB service).

## lab-sqlwin

- **Data disk:**
  - Fastest NVMe as `D:`, NTFS with a **64 KB** allocation unit.
  - Folders `D:\MSSQL\Data`, `D:\MSSQL\Log`, `D:\MSSQL\TempDB`, `D:\MSSQL\Backup`.
- **SQL Server 2025, Enterprise Developer edition (free):**
  - If your production runs SQL Server 2022, install 2022 instead and tell Claude.
  - Standard edition limits columnstore queries to 2 parallel threads; test 1.4 mimics that.
  - Install the latest cumulative update.
- **Installer choices:**
  - Features: *Database Engine Services* only.
  - Authentication: **Mixed mode**. Keep the `sa` password in your password manager.
  - Check **Grant Perform Volume Maintenance Task privilege** (instant file initialization).
  - Data, log, TempDB and backup folders on `D:` as above; TempDB file count as the installer suggests.
  - Keep the recommended max server memory; Claude sets the test values.
- **After install:**
  - SQL Server Configuration Manager: TCP/IP enabled on static port 1433; SQL Server Browser disabled.
  - Create SQL login `duckstore` and an empty database `duckstore` (recovery model SIMPLE), owned by that login.
  - Windows Defender: exclude `D:\MSSQL` and `sqlservr.exe` from real-time scanning.
  - Tools: `sqlcmd` (go-sqlcmd), `iperf3`; SSMS is optional, for you.
- **Ports:** 1433.
- **If `lab-sqlwin` dual boots with `lab-db`:** make it possible to boot the other OS once over SSH.
  - From Linux: `efibootmgr --bootnext`.
  - From Windows: `bcdedit /set {fwbootmgr} bootsequence`.
  - Write the exact commands in the send-back.

## lab-client

- **Base:** the Linux server base.
- **Software:**
  - .NET 10 SDK.
  - `postgresql-client-18` (PGDG apt repository).
  - `sqlcmd` (go-sqlcmd).
  - DuckDB CLI 1.5.x.
- **Ports:** 5085 (web app).

## Extra checks

- From `lab-client`: `sqlcmd -S <lab-sqlwin ip> -U duckstore -Q "SELECT @@VERSION"` works.
- If dual boot: switching `lab-db` to Windows and back over SSH works.

## Send back

Rows for `lab-db`, `lab-sqlwin` and `lab-client`, plus:
- The `SELECT @@VERSION` line.
- The OS switch commands, if dual boot.

## What Claude does

**Build first.** This can start on the laptop before the lab is ready:
- T-SQL versions of the 15 questions, for store tables and star schema.
- A loader that bulk-copies the data into SQL Server, then adds clustered columnstore indexes to the star schema.
- Two new approaches on the Compare page: SQL Server rowstore and SQL Server columnstore.

**Tests:**

| # | Test | Where | Answers |
|---|---|---|---|
| 1.1 | 15 questions on every engine, one engine running at a time, median of 5 runs | `lab-db`, run from `lab-client` | Which engine wins which kind of question on the same hardware? |
| 1.2 | Test 1.1 again with the same memory limit for every engine | `lab-db` | Is it the engine, or just more memory? |
| 1.3 | SQL Server questions on Windows vs on Linux | `lab-sqlwin` vs `lab-db` | Does the OS matter? |
| 1.4 | Columnstore questions with MAXDOP 1, 2 (like Standard edition), 4 and all cores | `lab-db`, `lab-sqlwin` | How much of the speed comes from parallelism? |
| 1.5 | Test 1.1 at scale 20 (50M orders) | `lab-db` | Where does each engine start to slow down? |
