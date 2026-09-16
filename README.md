# duckstore

A learning project that answers one question: **when should you use Postgres, and when should you use DuckDB?**

It is an online store, built twice over the same data:

```
            OLTP (many small reads/writes)            OLAP (big scans and aggregations)
┌──────────┐      ┌─────────────────────┐   ETL   ┌──────────────────────┐      ┌───────────────┐
│ Store UI │ ───▶ │ Postgres 18 (3NF)   │ ──────▶ │ DuckDB warehouse     │ ◀─── │ Analytics UI  │
│ (Go)     │      │ ~20 related tables  │         │ star schema + Parquet│      │ (Go)          │
└──────────┘      └─────────────────────┘         └──────────────────────┘      └───────────────┘
```

> Work in progress. The full guide is being written step by step.

## Stack

- Go (single binary with sub-commands)
- Postgres 18 in Docker (`pgx` driver)
- DuckDB 1.5 embedded in the Go process (`duckdb-go` driver)
