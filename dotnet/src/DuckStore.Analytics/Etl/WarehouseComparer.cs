using Dapper;
using DuckDB.NET.Data;

namespace DuckStore.Analytics.Etl;

// Proves that two warehouse files hold the same data, for example an incrementally updated warehouse
// and a full build from the same Postgres:
//
//   dotnet DuckStore.Analytics.dll compare-warehouses /data/warehouse.duckdb /data/full-check.duckdb
//
// For every raw.* and dw.* table: rows in A, rows in B, and how many rows are only in one of them
// (EXCEPT ALL, so duplicates count and row order does not matter). product_key is left out: a full ETL
// numbers product versions from 1, an incremental ETL keeps old keys and appends new ones. Instead, a
// last check compares which product VERSION (product_id, valid_from) every sale points to.
public static class WarehouseComparer
{
    private static readonly string[] Metadata = ["etl_info", "etl_steps", "etl_runs"];

    public static async Task<bool> RunAsync(string a, string b)
    {
        await using var duck = new DuckDBConnection("Data Source=:memory:");
        await duck.OpenAsync();
        await duck.ExecuteAsync($"ATTACH '{a.Replace("'", "''")}' AS a (READ_ONLY)");
        await duck.ExecuteAsync($"ATTACH '{b.Replace("'", "''")}' AS b (READ_ONLY)");

        var tables = (await duck.QueryAsync<(string Schema, string Table)>("""
            SELECT schema_name, table_name FROM duckdb_tables()
            WHERE database_name = 'a' AND schema_name IN ('raw', 'dw')
            ORDER BY schema_name, table_name
            """)).Where(t => !Metadata.Contains(t.Table)).ToList();

        Console.WriteLine($"{"table",-28} {"rows in A",12} {"rows in B",12} {"only in A",10} {"only in B",10}");
        var same = true;
        foreach (var (schema, table) in tables)
        {
            var columns = (await duck.QueryAsync<string>(
                "SELECT column_name FROM duckdb_columns() WHERE database_name = 'a' AND schema_name = $s AND table_name = $t AND column_name <> 'product_key' ORDER BY column_index",
                new { s = schema, t = table })).Select(c => $"\"{c}\"");
            var list = string.Join(", ", columns);
            var result = await duck.QuerySingleAsync<(long InA, long InB, long OnlyA, long OnlyB)>($"""
                SELECT (SELECT count(*) FROM a.{schema}.{table}),
                       (SELECT count(*) FROM b.{schema}.{table}),
                       (SELECT count(*) FROM (SELECT {list} FROM a.{schema}.{table} EXCEPT ALL SELECT {list} FROM b.{schema}.{table})),
                       (SELECT count(*) FROM (SELECT {list} FROM b.{schema}.{table} EXCEPT ALL SELECT {list} FROM a.{schema}.{table}))
                """);
            var ok = result.OnlyA == 0 && result.OnlyB == 0;
            same &= ok;
            Console.WriteLine($"{schema + "." + table,-28} {result.InA,12:N0} {result.InB,12:N0} {result.OnlyA,10:N0} {result.OnlyB,10:N0}{(ok ? "" : "  DIFFERENT")}");
            if (!ok)
            {
                // A few differing rows from each side, to see which columns differ.
                foreach (var (side, first, second) in new[] { ("A", "a", "b"), ("B", "b", "a") })
                {
                    await using var reader = await duck.ExecuteReaderAsync(
                        $"SELECT {list} FROM {first}.{schema}.{table} EXCEPT ALL SELECT {list} FROM {second}.{schema}.{table} ORDER BY ALL LIMIT 3");
                    while (await reader.ReadAsync())
                    {
                        var cells = Enumerable.Range(0, reader.FieldCount).Select(i => $"{reader.GetName(i)}={(reader.IsDBNull(i) ? "NULL" : reader.GetValue(i))}");
                        Console.WriteLine($"    only in {side}: {string.Join(", ", cells)}");
                    }
                }
            }
        }

        // The version every sale points to, compared by business key instead of surrogate key.
        const string versions = """
            SELECT s.order_id, s.line_no, d.product_id, d.valid_from
            FROM {0}.dw.fact_sales s JOIN {0}.dw.dim_product d USING (product_key)
            """;
        var wrongVersion = await duck.ExecuteScalarAsync<long>($"""
            SELECT count(*) FROM ({string.Format(versions, "a")} EXCEPT ALL {string.Format(versions, "b")})
            """);
        same &= wrongVersion == 0;
        Console.WriteLine($"{"sales -> product version",-28} {"",12} {"",12} {wrongVersion,10:N0}{(wrongVersion == 0 ? "" : "  DIFFERENT")}");
        Console.WriteLine(same ? "The two warehouses hold the same data." : "The warehouses are DIFFERENT.");
        return same;
    }
}
