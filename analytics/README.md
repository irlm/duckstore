# Compare questions

One folder per question on the Compare page. Each file is the SQL one engine runs on one data model:

| File | Data model | Runs on |
|---|---|---|
| `store.sql` | the normalized store tables, as the application writes them | Postgres (`store.*`) and DuckDB (the ETL's `raw.*` copy of the same tables) |
| `star.sql` | the star schema the ETL builds | Postgres (`dw.*` copy) and DuckDB |
| `star.duckdb.sql` + `star.postgres.sql` | the star schema, when the two SQL dialects differ | one engine each |

`store.sql` is written for Postgres. The analytics service runs the same text on DuckDB, after two
replacements: the schema `store.` becomes `raw.`, and parameters `@name` become `$name`. So the store
tables give the **engine** effect (same SQL, same tables), and store vs star gives the **data model** effect.

The SQL in `store.sql` only counts orders up to `@max_order_id`, the warehouse watermark, so all four
answers use the same orders. The page checks that they are equal.

When `star.duckdb.sql` and `star.postgres.sql` both exist, compare them: the first line says what is
different (QUALIFY, list functions, `quantile_cont`, `date_diff`, interval syntax, parameter names).
