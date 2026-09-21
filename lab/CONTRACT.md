# Contract between duckstore and NetworkLab

**Version 1 — 2026-09-21.** The same text lives in both repositories:
[duckstore `lab/CONTRACT.md`](https://github.com/irlm/duckstore/blob/main/lab/CONTRACT.md) and
[NetworkLab `docs/duckstore-contract.md`](https://github.com/irlm/NetworkLab/blob/main/docs/duckstore-contract.md).
Change both in the same step and raise the version, or the two sides drift.

## Who owns what

| | duckstore — *what* is measured | NetworkLab — *where* it runs |
|---|---|---|
| Owns | the application, schema, seed, ETL; the 15 questions in three SQL dialects; the Compare page and the load test; `bench/` (run, verify, local runner, charts); the write-ups in `docs/results/` | the machines, PXE, OS installs and drivers; Docker and engines on the hosts; secrets; Prometheus and Grafana; collecting results; its own workloads (TPC-H `duckbench`, fib, network diagnostics) |
| Changes without asking | anything inside duckstore, as long as the interface below still holds | anything inside the lab, as long as the interface below still holds |
| Must ask the other side | a new host requirement (a package, a port, a privilege) | renaming a host, a secret, a results path or a TSV column |

**Local first.** duckstore develops and debugs every experiment on local Docker, and only runs a method on the lab
once it works there. A lab night is too expensive to find a stdin bug or a stale image.

## The interface

### Hosts

Roles come from [lab/setup-1-duckdb-sqlserver-postgres.md](setup-1-duckdb-sqlserver-postgres.md). Machines come
from NetworkLab's `lab-setup/scripts/lab-nodes.conf`. Today:

| role | host | address | notes |
|---|---|---|---|
| lab-db | turing | 172.16.48.223 | Postgres, pg_duckdb, SQL Server on Linux, DuckDB; 12 threads, 62 GB |
| lab-sqlwin | LAB02 | lab subnet | SQL Server 2025 on Windows Server 2025 |
| second engine host | LAB03 | 172.16.48.181 | same hardware as LAB02, for controlled comparisons |
| lab-client | LAB04 | 172.16.48.159 | runs the benchmark clients |
| load generators | node01–04 | 172.16.48.148–248 | Raspberry Pi 5 |
| results + monitoring | LAB05 | lab subnet | Prometheus, Grafana, `/srv/duckbench-results` |
| bridge | lnode02 | 172.16.48.2 / 10.10.11.110 | the only route between the lab and the home network; SSH jump host |
| laptop | reef | 10.10.10.171 | where duckstore is developed; scraped via `LAB_MONITOR_EXTRA` |

duckstore reads host names from `bench/bench.conf` (`BENCH_PG_HOST`, `BENCH_MSSQL_HOST`, …) and reaches the lab
through `BENCH_SSH_JUMP=lnode02`.

### What an engine host provides

- Docker Engine and the compose plugin. The benchmark user is in the `docker` group.
- A fast data disk at `/data` (XFS, so reflink copies work).
- **Passwordless sudo for dropping the page cache**, so `--cold` can mean cold:
  `ALL=(root) NOPASSWD: /usr/bin/sh -c echo\ 3\ >\ /proc/sys/vm/drop_caches`.
  Without it, only `--cold-engine` is possible, and results must say so.
- node_exporter on 9100 (windows_exporter on 9182), listed in Prometheus's targets.
- Ports as in the setup file:
  - Postgres 5432
  - SQL Server 1433
  - DuckDB service 5090
  - web app 5085

  On the laptop the first two are 55432 and 51433 to avoid clashes.

### Secrets

- **Where they live:** `/etc/lab-secrets.env`, mode 600, on the host that needs them. On the laptop: `.env` and
  `lab/lab.env`, both git-ignored.
- **Never on a command line**, and never in either repository.
- **Names both sides use:**
  - `DUCKBENCH_PG_PASSWORD` and `PGPASSWORD`
  - `SQLCMDPASSWORD`
  - `LAB_SQL_SA_PASSWORD`: needed for cold SQL Server runs, `DBCC DROPCLEANBUFFERS`
  - `LAB_SQL_DUCKSTORE_PASSWORD`
  - `GRAFANA_ADMIN_PASSWORD`

### Results

- **One run per timed repetition**, tab separated, **no header**, in the home directory as `results-*.tsv`.
- **Two row shapes**, identical in both repos:
  - `iso_time  host  engine  scale  query  rep  ms` — engine-reported time, one process
  - `iso_time  host  engine  worker  query  ms` — concurrent clients, end-to-end time
- **Engine names:** `duckdb`, `postgres`, `pgduckdb`, `mssql`.
- **Query labels:**
  - duckstore: `<question>.<model>`, e.g. `monthly-revenue.star`
  - TPC-H: `q01` … `q22`
- **File names:** duckstore writes `results-duckstore-<engine>-<stamp>.tsv`.
- **Collection:** NetworkLab collects with
  `duckbench-collect.sh --label <name> --hosts <user@host,…>` into `/srv/duckbench-results/<label>-<stamp>/`.

### Correctness before timing

No number is published unless the engines were checked to return the same rows first:
- duckstore: `bench/duckstore-bench-verify.sh`
- NetworkLab: `duckbench-verify.sh`

A run that could not empty the caches it claims to have emptied is refused, not reported.

## Rules for sharing the lab

1. **One benchmark per engine host at a time.** Two engines on one box is exactly the interference the lab exists
   to avoid. *Proposed for v2:* both sides take `flock /data/bench.lock` on an engine host before a run and hold it
   until the last row is written.
2. **Check Grafana before starting.** A host above ~10% CPU is busy with someone else's run.
3. **Say what ran.** *Proposed for v2:* each run posts a Grafana annotation (start, end, label), so a sweep is
   framed on the graphs instead of found by timestamp — already on NetworkLab's open list.
4. **Leave the host as you found it:**
   - engines stopped, or back at their documented settings;
   - tuning scripts undone (every duckstore tuning has a `-drop` script);
   - no stray containers.

## Open items

| item | owner | status |
|---|---|---|
| `bench/duckstore-bench-lab.sh`: deploy to lab hosts over SSH, run, leave TSVs for the collector | duckstore | to do |
| the `flock` convention on engine hosts | both | proposed |
| Grafana run annotations | both | proposed |
| passwordless `drop_caches` on every engine host | NetworkLab | check per host |
| DHCP reservation for the laptop (10.10.10.171) so its Prometheus target stays valid | home network | to do |
