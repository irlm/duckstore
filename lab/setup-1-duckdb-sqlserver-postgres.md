# Setup 1: DuckDB vs SQL Server vs Postgres

Machines, OS and software for comparing DuckDB, SQL Server and Postgres (plus pg_duckdb) on real servers.

**The setup agent installs only what is listed here.** Claude copies the app later, loads the data and creates the
test scenarios from the IPs.

## Machines

| Hostname | Pick the machine with | OS | Runs |
|---|---|---|---|
| `lab-db` | Most RAM and the fastest NVMe. Min 32 GB RAM (64 GB+ is better), 8+ cores, **1 TB free** on NVMe | Ubuntu Server 24.04 LTS | Postgres 18 + pg_duckdb, SQL Server on Linux, DuckDB service. All as containers, one at a time, so they compare on the same hardware |
| `lab-sqlwin` | Best: the `lab-db` machine itself, with Windows on a second disk (dual boot). Otherwise: x64 as close to `lab-db` as possible, min 32 GB RAM, 500 GB free on NVMe | Windows Server 2025 Standard, Desktop Experience (evaluation is OK) | SQL Server as companies run it, and the DuckDB service on the same hardware |
| `lab-client` | Any x64 with 8+ threads and 16 GB RAM. **Not** one of the database machines | Ubuntu Server 24.04 LTS | Web app, Compare and load test runs |

- A machine can have more than one role; send the same IP for each role.
- `lab-db` needs 1 TB for three reasons:
  - Larger test data is 4× the laptop data.
  - Every engine keeps its own copy.
  - One pg_duckdb query wrote 196 GB of temp files on the laptop.

## Every machine

- **Network:**
  - Wired Ethernet, same switch and subnet, no Wi-Fi.
  - A fixed IP (DHCP reservation).
  - The fastest link the machine has (1 GbE minimum).
- **Always on:**
  - Never sleep.
  - High-performance power plan (Linux: CPU governor `performance`).
  - Time sync on.
- **Updates:** install all updates first, then pause automatic updates while tests run.
- **SSH for Claude:**
  - User `duck` with key login; authorize the laptop's public key `~/.ssh/id_ed25519.pub`.
  - Windows: enable OpenSSH Server. `duck` is an administrator, so the key goes in
    `C:\ProgramData\ssh\administrators_authorized_keys`.
- **Firewall:** open only the ports listed below, plus 22 (SSH) and 5201 (iperf3). Allow them only from the lab subnet.
- **No GitHub credentials** on lab machines. Claude copies the repo with `rsync` over SSH.
- **No passwords in the repo or in chat.** Put them in `lab/lab.env` on the laptop (git-ignored).
- **Tools:** `iperf3` on every machine.

## lab-db and lab-client (Linux)

- **OS:** Ubuntu Server 24.04 LTS, minimal, no desktop.
- **Data disk:** the fastest NVMe, formatted **XFS** and mounted at `/data`. XFS supports reflinks, so the ETL snapshot
  copy takes milliseconds.
- **Docker:**
  - Docker Engine and the compose plugin, from Docker's apt repository (not snap).
  - Set `"data-root": "/data/docker"` in `/etc/docker/daemon.json`.
- **User `duck`:** passwordless sudo (OK only in this private lab), and member of the `docker` group.
- **Packages:** `git rsync curl jq htop sysstat fio iperf3`.
- **No database packages on the host.** Claude runs Postgres and SQL Server as containers, with the same images and
  settings as the laptop.

### lab-db only

- **Optional pre-pull:** `postgres:18`, `pgduckdb/pgduckdb:18-v1.1.1`, `mcr.microsoft.com/mssql/server:2025-latest`.
- **Ports:** 5432 (Postgres), 1433 (SQL Server), 5090 (DuckDB service).

### lab-client only

- **Software:**
  - .NET 10 SDK.
  - `postgresql-client-18` (PGDG apt repository).
  - `sqlcmd` (go-sqlcmd).
  - DuckDB CLI 1.5.x.
- **Ports:** 5085 (web app).

## lab-sqlwin (Windows)

- **Data disk:**
  - Fastest NVMe as `D:`, NTFS with a **64 KB** allocation unit.
  - Folders `D:\MSSQL\Data`, `D:\MSSQL\Log`, `D:\MSSQL\TempDB`, `D:\MSSQL\Backup` and `D:\duckstore`.
- **SQL Server 2025, Enterprise Developer edition (free):**
  - If your production runs SQL Server 2022, install 2022 instead and tell Claude.
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
  - Windows Defender: exclude `D:\MSSQL`, `D:\duckstore` and `sqlservr.exe` from real-time scanning.
- **Software:**
  - .NET 10 SDK, for the DuckDB service.
  - `sqlcmd` (go-sqlcmd).
  - `iperf3`.
  - SSMS is optional, for you.
- **Ports:** 1433 (SQL Server), 5090 (DuckDB service).
- **If `lab-sqlwin` dual boots with `lab-db`:** make it possible to boot the other OS once over SSH.
  - From Linux: `efibootmgr --bootnext`.
  - From Windows: `bcdedit /set {fwbootmgr} bootsequence`.
  - Write the exact commands in the send-back.

## Checks when done

1. Run `iperf3 -s` on every machine.
2. From `lab-client`, for each other machine:
   - `ping -c 20 <ip>`: the average should be under 0.5 ms.
   - `iperf3 -c <ip> -t 10`: the result should be close to the link speed.
3. From the laptop: `ssh duck@<ip> hostname` works for every machine.
4. From `lab-client`: `sqlcmd -S <lab-sqlwin ip> -U duckstore -Q "SELECT @@VERSION"` works.
5. If dual boot: switching `lab-db` to Windows and back over SSH works.

## Send back

One row per role (same IP if a machine has more than one role):

| Role | Hostname | IP | CPU model | Cores / threads | RAM | Data disk (model, size) | Link speed | OS version | ping avg / iperf3 |
|---|---|---|---|---|---|---|---|---|---|
| lab-db | | | | | | | | | |
| lab-sqlwin | | | | | | | | | |
| lab-client | | | | | | | | | |

Also send:
- The `SELECT @@VERSION` line.
- The OS switch commands, if dual boot.
