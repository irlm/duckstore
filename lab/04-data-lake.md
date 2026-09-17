# Setup 4: Data lake

**Question:** Can DuckDB processes on several machines read and write one shared copy of the data, with no warehouse
server?

**Architecture:** Parquet files in S3-compatible object storage, with the DuckLake catalog in Postgres.

```
lab-db (Postgres) ──ETL──► lab-storage (Parquet files, S3 API)
lab-db (DuckLake catalog)          ▲
                                   └── DuckDB on lab-mac, lab-client, lab-replica (read and write)
```

Needs setup 1. Uses `lab-mac` and `lab-replica` as extra readers if setups 2 and 3 are done. Adds `lab-storage`.
Apply [00-every-machine.md](00-every-machine.md) first.

## Machines

| Hostname | Pick the machine with | OS |
|---|---|---|
| `lab-storage` | The fastest network link in the lab (10 GbE if there is one) and 500 GB free on SSD. Can be the same machine as `lab-spare` | Ubuntu Server 24.04 LTS |

## lab-storage

- **Base:** the Linux server base.
- **No object-storage software from the setup agent.** Claude runs an S3-compatible server as a container.
- **Ports:** 9000 (S3 API), 9001 (storage web console).

## Extra checks

- `iperf3` from each reader (`lab-mac`, `lab-client`, `lab-replica`) to `lab-storage`.

## Send back

- The row for `lab-storage`.
- The iperf3 result from each reader.

## What Claude does

**Tests:**

| # | Test | Answers |
|---|---|---|
| 4.1 | The 15 questions on a DuckDB file vs Parquet on a local disk vs Parquet on `lab-storage` | What do files and the network cost? |
| 4.2 | 1, 2 and 3 readers at once, on different machines | Does reading scale out across machines? |
| 4.3 | DuckLake with two writers on different machines while readers run queries | How does a lakehouse get around DuckDB's one-writer limit? |
| 4.4 | Test 4.1 on 1 GbE vs a faster link (if the lab has one) | Is the network the bottleneck? |
