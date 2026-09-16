# DuckDB for SQL Server developers

You already know relational modeling, joins, window functions and query plans. DuckDB uses all of that.
What changes is **where the database lives** and **how it stores data**. Every example on this page was
checked on DuckDB 1.5.5.

## The big picture

| | SQL Server | DuckDB |
|---|---|---|
| Runs as | A service; clients connect over TDS (port 1433) | A library inside your program; no port, no service |
| Stored in | .mdf/.ldf files managed by the server | One `.duckdb` file (plus a `.wal` while writing), or in memory |
| Security | Logins, users, roles, permissions | None: whoever can open the file can read it |
| Storage | Rowstore by default; columnstore indexes are optional | Always columnar, compressed per column segment |
| Execution | Row mode; batch mode with columnstore | Always vectorized (batches of ~2,000 values), on all cores |
| Writers | Many sessions, lock-based (or row versioning) | One process may write; inside it, optimistic MVCC |
| Tools | SSMS, Azure Data Studio | `duckdb` CLI, DBeaver, the DuckDB UI (`CALL start_ui()`), this project's SQL console |

Think "columnstore index + batch mode, packaged as a library, for one application at a time".

## Data types

| SQL Server | DuckDB | Notes |
|---|---|---|
| `int`, `bigint`, `smallint`, `tinyint` | `INTEGER`, `BIGINT`, `SMALLINT`, `TINYINT` | Also unsigned types (`UINTEGER`...) and `HUGEINT` |
| `decimal(p,s)` | `DECIMAL(p,s)` | Exact, up to 38 digits |
| `float` | `DOUBLE` | |
| `nvarchar(n)`, `varchar(max)` | `VARCHAR` | Always UTF-8; no length needed and no penalty for leaving it out |
| `bit` | `BOOLEAN` | Real `true`/`false` |
| `datetime2` | `TIMESTAMP` | |
| `datetimeoffset` | `TIMESTAMPTZ` | Displayed in the session `TimeZone` |
| `date`, `time` | `DATE`, `TIME` | `DATE - DATE` gives days; `DATE - 3` goes back 3 days |
| `uniqueidentifier` | `UUID` | |
| `varbinary` | `BLOB` | |
| JSON in `nvarchar` | `JSON` | With `->`, `->>` and `json_*` functions |
| table-valued parameters, XML | `LIST`, `STRUCT`, `MAP` | Real nested types (lesson 06) |
| `IDENTITY(1,1)` | `CREATE SEQUENCE s; ... DEFAULT nextval('s')` | |

## Syntax, side by side

| T-SQL | DuckDB |
|---|---|
| `SELECT TOP 10 ...` | `SELECT ... LIMIT 10` |
| `SELECT * FROM t` | `FROM t` also works |
| `ISNULL(a, b)` | `coalesce(a, b)` (or `ifnull`) |
| `IIF(c, a, b)` | `CASE WHEN ...` or `if(c, a, b)` |
| `GETDATE()`, `SYSDATETIME()` | `now()`, `current_timestamp`, `current_date` |
| `DATEADD(day, 7, d)` | `d + INTERVAL 7 DAY` |
| `DATEDIFF(day, a, b)` | `date_diff('day', a, b)` |
| `CONVERT(int, x)` | `CAST(x AS INTEGER)` or `x::INTEGER`; `TRY_CAST` exists |
| `a + b` for strings | `a \|\| b` or `concat(a, b)` |
| `LEN(s)`, `CHARINDEX(x, s)` | `length(s)`, `strpos(s, x)` |
| `STRING_AGG(x, ',')` | `string_agg(x, ',')`, or `list(x)` for a real list |
| `SELECT ... GROUP BY a, b, c` | `GROUP BY ALL` |
| `ROW_NUMBER()` in a CTE, then `WHERE rn = 1` | `QUALIFY row_number() OVER (...) = 1` |
| `OUTER APPLY (SELECT TOP 1 ... ORDER BY date DESC)` | `ASOF LEFT JOIN ... ON ... AND a.date >= b.date` |
| `CROSS APPLY` | `LATERAL` (DuckDB also detects it without the keyword) |
| `PIVOT (... FOR y IN ([2024],[2025]))` | `PIVOT t ON y USING sum(x)`: no value list needed |
| `#temp` table | `CREATE TEMP TABLE` |
| Scalar function / inline TVF | `CREATE MACRO f(a) AS ...` / `CREATE MACRO f(a) AS TABLE SELECT ...` |
| Stored procedure, trigger | Not available: the logic lives in the application |
| `MERGE` | `MERGE INTO`, or `INSERT ... ON CONFLICT DO UPDATE` |
| `BULK INSERT`, `OPENROWSET(BULK ...)` | `FROM 'file.parquet'`, `read_csv(...)`, `COPY t FROM 'file.csv'` |
| `bcp ... out` | `COPY (SELECT ...) TO 'file.parquet' (FORMAT parquet)` |
| Linked server | `ATTACH '...' AS pg (TYPE postgres)`, also `mysql` and `sqlite` |
| `sp_help 't'` | `DESCRIBE t`; `SUMMARIZE t` profiles every column |
| `sys.tables`, `sys.columns` | `duckdb_tables()`, `duckdb_columns()`, `information_schema` |
| `SET STATISTICS TIME ON` + actual plan | `EXPLAIN ANALYZE` |
| `GO` | `;` |

## Things that surprise SQL Server developers

1. **String comparison is case-sensitive.** `'abc' = 'ABC'` is `false`. SQL Server's default collation is
   case-insensitive. Use `ILIKE`, `lower()`, or `COLLATE NOCASE`.
2. **Integer division returns a decimal result.** `1 / 2` is `0.5`, not `0`. Use `//` for integer division (`7 // 2` = 3).
3. **NULLs sort last** in ascending order. SQL Server sorts them first. Write `NULLS FIRST` when it matters.
4. **Aliases can be reused** in the same `SELECT`, in `WHERE` and in `GROUP BY`:
   `SELECT price * qty AS gross, gross * 0.2 AS tax ...`.
5. **Lists are 1-based**: `[10, 20, 30][1]` is `10`.
6. **No indexes to tune, mostly.** DuckDB keeps min/max statistics per row group ("zone maps") and skips
   groups that cannot match. Loading data **sorted** by the column you filter on most (usually a date) makes
   that work, like choosing a clustered index. `PRIMARY KEY` and `UNIQUE` create ART indexes, which cost memory
   and slow bulk loads; warehouses usually leave them out.
7. **Constraints are lighter.** `PRIMARY KEY`, `UNIQUE`, `NOT NULL` and `CHECK` work. Foreign keys exist but have
   no cascading actions. In a warehouse the ETL guarantees consistency instead.
8. **Many small transactions are slow, big statements are fast.** See the engine race: 5,000 single-row inserts
   run 4× slower than in Postgres, while a 5-million-row `INSERT ... SELECT` runs 10× faster.
9. **One writer process.** A second process that opens the same file read-write gets a lock error. Inside one
   process, two transactions that change the same row do not wait: one of them fails with a conflict.
10. **Some combinations are not supported yet.** For example `GROUP BY ALL` together with `QUALIFY` (this project's ETL
    lists the columns instead), or `ORDER BY` on an alias of a struct field.

## Where to practice

Open the SQL console at `/analytics/sql` and load the lessons in order:

| Lesson | Topic |
|---|---|
| 01 | Friendly SQL: FROM-first, DESCRIBE, SUMMARIZE, GROUP BY ALL, EXCLUDE/REPLACE, COLUMNS() |
| 02 | Window functions, QUALIFY, named windows, arg_max, ntile |
| 03 | Recursive CTEs: category tree, org chart, referral chains |
| 04 | ASOF joins, SCD Type 2, point-in-time queries, filling missing days |
| 05 | PIVOT and UNPIVOT without dynamic SQL |
| 06 | LIST, STRUCT, MAP, lambdas and JSON |
| 07 | Parquet and CSV files: partitions, metadata, joins across files |
| 08 | Storage, compression, zone maps and plans |
| 09 | Querying live Postgres from DuckDB, hybrid queries, `postgres_query` |
| 10 | The Postgres side for SQL Server developers: DISTINCT ON, LATERAL, jsonb, sizes, index usage |
| 11 | Macros, temp tables and transactions |
| 12 | Exercises |
