# Lab setup

Machines to prepare for the duckstore lab tests: SQL Server, Postgres and DuckDB on real servers.

**The setup agent installs the OS and the software below and nothing else.** Claude copies the app later, loads the
data and runs the tests.

## Machines

A machine can take more than one role. If it does, give each role the same IP. Keep the **client** off the
database machines, because the load generator would steal their CPU.

| Role | Hostname | Pick the machine with | OS | Used for |
|---|---|---|---|---|
| **db-linux** | `lab-db` | Most RAM and the fastest NVMe. Min 32 GB RAM (64 GB+ is better), 8+ cores, 1 TB free on NVMe | Ubuntu Server 24.04 LTS | Postgres 18 + pg_duckdb, SQL Server on Linux, DuckDB service. One engine at a time, so they compare on the same hardware |
| **sql-windows** | `lab-sqlwin` | x64, as close to db-linux as possible. Min 32 GB RAM, 500 GB free on NVMe | Windows Server 2025 Standard (evaluation is OK) | SQL Server as companies run it; Windows vs Linux |
| **analytics-mac** | `lab-mac` | The Mac mini | Latest macOS it supports | DuckDB service, run natively (no Docker) |
| **client** | `lab-client` | Any x64 with 8+ threads and 16 GB RAM | Ubuntu Server 24.04 LTS | Web app, Compare and load test runs |
| **spare** (optional) | `lab-spare` | Any other machine | Ubuntu Server 24.04 LTS | The same tests on different hardware |

The best sql-windows is the db-linux machine itself, booting from a second disk, so both OSes share the same
hardware. A different machine is OK too; then we compare only ratios between the two.

## Every machine

- **Network:**
  - Wired Ethernet, all on the same switch and subnet. No Wi-Fi.
  - A fixed IP (DHCP reservation).
  - The fastest link each machine has (1 GbE minimum).
- **Always on:**
  - Never sleep.
  - High-performance power plan (Linux: CPU governor `performance`).
  - Time sync on.
- **Updates:** install all updates first, then pause automatic updates while tests run.
- **SSH for Claude:**
  - User `duck` with key login.
  - Authorize the laptop's public key `~/.ssh/id_ed25519.pub`.
  - Linux: `duck` gets passwordless sudo and joins the `docker` group. This is OK only because this is a private lab.
- **Firewall:** open only the ports listed below, and only to the lab subnet. Nothing exposed to the internet.
- **No GitHub credentials** on the lab machines. Claude copies the repo with `rsync` over SSH.
- **No passwords in the repo or in chat.** Put them in `lab/lab.env` on the laptop (git-ignored).
- **Tools:** `iperf3` on every machine.

## db-linux (and spare)

- **OS:** Ubuntu Server 24.04 LTS, minimal, no desktop.
- **Data disk:**
  - Fastest NVMe, formatted **XFS** and mounted at `/data`.
  - XFS supports reflinks, so the ETL snapshot copy takes milliseconds, like btrfs on the laptop.
- **Docker:**
  - Docker Engine and the compose plugin, from Docker's apt repository (not snap).
  - Set `"data-root": "/data/docker"` in `/etc/docker/daemon.json`.
- **Packages:** `git rsync curl jq htop sysstat fio iperf3`.
- **Do not install Postgres or SQL Server packages on the host.** Claude runs both as containers, with the same images
  and settings as the laptop, one at a time.
- **Optional pre-pull:** `postgres:18`, `pgduckdb/pgduckdb:18-v1.1.1`, `mcr.microsoft.com/mssql/server:2025-latest`.
- **Ports:** 22 (SSH), 5432 (Postgres), 1433 (SQL Server), 5090 (analytics service), 5201 (iperf3).

## sql-windows

- **OS:** Windows Server 2025 Standard, Desktop Experience.
- **Data disk:**
  - Fastest NVMe as `D:`, NTFS with a **64 KB** allocation unit.
  - Folders `D:\MSSQL\Data`, `D:\MSSQL\Log`, `D:\MSSQL\TempDB`, `D:\MSSQL\Backup`.
- **SQL Server 2025, Enterprise Developer edition (free).**
  - If your production runs SQL Server 2022, install 2022 instead and tell Claude.
  - Standard edition limits columnstore queries to 2 parallel threads. Claude can mimic that later.
  - Install the latest cumulative update.
- **Installer choices:**
  - Features: *Database Engine Services* only.
  - Authentication: **Mixed mode**. Keep the `sa` password in your password manager.
  - Check **Grant Perform Volume Maintenance Task privilege** (instant file initialization).
  - Data, log, TempDB and backup folders on `D:` as above; TempDB file count as the installer suggests.
  - Keep the recommended max server memory; Claude sets the test values.
- **After install:**
  - SQL Server Configuration Manager: TCP/IP enabled on static port 1433; SQL Server Browser disabled.
  - Create SQL login `duckstore` and an empty database `duckstore` (recovery model SIMPLE) owned by that login.
  - Windows Defender: exclude `D:\MSSQL` and `sqlservr.exe` from real-time scanning.
  - Enable **OpenSSH Server**; put the laptop key in `C:\ProgramData\ssh\administrators_authorized_keys` for `duck`
    (an administrator).
  - Tools: `sqlcmd` (go-sqlcmd), `iperf3`; SSMS optional, for you.
- **Ports:** 22, 1433, 5201.

## analytics-mac

- **Power and access:**
  - Energy: never sleep, start automatically after a power failure.
  - Sharing: **Remote Login** on for `duck`, key login.
- **Software:**
  - Homebrew, then `brew install git duckdb iperf3`.
  - .NET 10 SDK (Microsoft installer or `brew install --cask dotnet-sdk`).
- **Folder:** `~/duckstore-data` on the internal SSD.
- **Firewall:** allow incoming connections on port 5090, or turn the macOS firewall off (lab only).
- **Do not install Docker.** Docker on macOS runs inside a VM, and we want native speed.

## client

- **OS:** Ubuntu Server 24.04 LTS.
- **Docker:** Docker Engine and the compose plugin, from Docker's apt repository.
- **Software:**
  - .NET 10 SDK.
  - `postgresql-client-18` (PGDG apt repository).
  - `sqlcmd` (go-sqlcmd).
  - DuckDB CLI 1.5.x.
  - `git rsync curl jq htop iperf3`.
- **Ports:** 22, 5085 (web app), 5201.

## Final check (setup agent)

1. Run `iperf3 -s` on each machine.
2. From the client, for each other machine:
   - `ping -c 20 <ip>`: the average should be under 0.5 ms.
   - `iperf3 -c <ip> -t 10`: the result should be close to the link speed.
3. From the laptop: `ssh duck@<ip> hostname` works for every machine.
4. From the client: `sqlcmd -S <sql-windows ip> -U duckstore -Q "SELECT @@VERSION"` works.

## Send back when done

Fill in this table. Leave passwords out; they go in `lab/lab.env` on the laptop.

| Role | Hostname | IP | CPU model | Cores / threads | RAM | Data disk (model, size) | Link speed | OS version | ping avg / iperf3 |
|---|---|---|---|---|---|---|---|---|---|
| db-linux | | | | | | | | | |
| sql-windows | | | | | | | | | |
| analytics-mac | | | | | | | | | |
| client | | | | | | | | | |
| spare | | | | | | | | | |

Also send:
- Which roles share a machine.
- The SQL Server version line from `SELECT @@VERSION`.

## What Claude does next

With the IPs, Claude writes `lab/hosts.env`, copies the repo, builds the images, loads scale 5 (Postgres, then SQL
Server rowstore and columnstore) and runs these scenarios:

| # | Scenario | Question it answers |
|---|---|---|
| S1 | All engines on db-linux, one at a time; client on its own machine | Which engine is faster on the same hardware? |
| S2 | Postgres on db-linux, DuckDB service on the Mac, load test from the client | Do separate servers keep the store fast while reports run? |
| S3 | The same SQL Server tests on Windows and on Linux | Does the OS matter? |
| S4 | S1 on the spare machine | Do the ratios hold on different hardware? |
| S5 | S1 at scale 20 (50M orders) | Where does each engine start to slow down? |
