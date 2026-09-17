# Setup 2: Analytics server

**Question:** When reports run on their own server, does the store stay fast? How many report users can one DuckDB
node serve?

**Architecture:** the DuckDB service on its own machine. The laptop only simulated this by giving containers separate
CPU cores.

```
lab-client ──store traffic──► lab-db (Postgres) ──ETL over the network──► lab-mac (DuckDB service)
           └──reports────────────────────────────────────────────────────►
```

Needs setup 1 (`lab-db`, `lab-client`). Adds `lab-mac`. Apply [00-every-machine.md](00-every-machine.md) first.

## Machines

| Hostname | Pick the machine with | OS |
|---|---|---|
| `lab-mac` | The Mac mini | The latest macOS it supports |

## lab-mac

- **Energy:** never sleep, and start automatically after a power failure.
- **Software:**
  - Homebrew, then `brew install git duckdb iperf3`.
  - .NET 10 SDK (Microsoft installer or `brew install --cask dotnet-sdk`).
- **Folder:** `~/duckstore-data` on the internal SSD.
- **Firewall:** allow incoming connections on port 5090, or turn the macOS firewall off (lab only).
- **Do not install Docker.** Docker on macOS runs inside a VM, and this setup needs native speed.
- **Ports:** 5090 (DuckDB service).

## Extra checks

- `iperf3` between `lab-db` and `lab-mac` in both directions. The ETL uses this path.

## Send back

- The row for `lab-mac`.
- The `lab-db` ↔ `lab-mac` iperf3 results.

## What Claude does

**Build first:**
- A report concurrency test.
- A macOS version of the ETL snapshot copy (`cp -c` instead of `cp --reflink`).

**Tests:**

| # | Test | Answers |
|---|---|---|
| 2.1 | Load test: store traffic from `lab-client` to Postgres on `lab-db`. Reports on the same Postgres vs on the DuckDB service on `lab-mac` (and on SQL Server columnstore on `lab-sqlwin`, if it is its own machine) | Does a separate server protect the store? |
| 2.2 | Report users 1, 4, 16 and 32 on the DuckDB service | Where does one DuckDB node stop scaling? |
| 2.3 | Full and incremental ETL from `lab-db` to `lab-mac` | What does the network add to the ETL? |
| 2.4 | Lookups and large results over the real network | Do the laptop's Toxiproxy results hold? |
