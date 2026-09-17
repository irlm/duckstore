# Lab

Tests on real machines instead of containers on one laptop. The laptop results are in
[dotnet/README.md](../dotnet/README.md).

**One file per setup.** A setup is a set of machines, operating systems and software, named
`setup-<n>-<what it compares>.md`. When a later scenario needs a different OS or different software, it gets a new
setup file.

## How it works

1. Give a setup file to the setup agent. It installs the OS and software, runs the checks and fills in the send-back
   table.
2. Send the table to Claude. Claude creates the test scenarios from the IPs; a machine with two roles uses the same IP.
3. Results go in `docs/results/lab/`.

## Setups

| File | Compares | Machines | Status |
|---|---|---|---|
| [setup-1-duckdb-sqlserver-postgres.md](setup-1-duckdb-sqlserver-postgres.md) | DuckDB vs SQL Server vs Postgres (plus pg_duckdb), on Linux and on Windows | lab-db, lab-sqlwin, lab-client | Waiting for install |
