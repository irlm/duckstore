using System.Diagnostics;
using System.Reflection;
using DuckDB.NET.Data;
using DuckStore.Analytics.Warehouse;
using DuckStore.Contracts;

namespace DuckStore.Analytics.Analytics;

// Runs the DuckDB side of the Compare page: analytics/<id>.duckdb.sql, embedded
// at build time. The web app runs the matching <id>.postgres.sql itself.
public sealed class AnalyticsRunner(DuckDbWarehouse warehouse)
{
    public const int MaxRows = 250_000;

    private static readonly Dictionary<string, string> Sql = LoadSql();

    public static string SqlFor(string id) =>
        Sql.TryGetValue(id, out var sql) ? sql : throw new KeyNotFoundException($"Unknown analytic '{id}'.");

    public static IReadOnlyCollection<string> Ids => Sql.Keys;

    public async Task<AnalyticResult> RunAsync(string id, AnalyticRequest request, CancellationToken ct)
    {
        var sql = SqlFor(id);
        await using var connection = await warehouse.OpenAsync(ct); // not timed, like a pooled Postgres connection
        await using var command = connection.CreateCommand();
        command.CommandText = sql;
        if (sql.Contains("$customer_id", StringComparison.Ordinal))
        {
            command.Parameters.Add(new DuckDBParameter("customer_id", request.CustomerId ?? 0));
        }

        var clock = Stopwatch.StartNew();
        await using var reader = await command.ExecuteReaderAsync(ct);
        var hasRow = await reader.ReadAsync(ct);
        var executeMs = clock.Elapsed.TotalMilliseconds;

        var columns = Enumerable.Range(0, reader.FieldCount).Select(reader.GetName).ToList();
        var rows = new List<object?[]>();
        long count = 0;
        while (hasRow)
        {
            count++;
            if (rows.Count < MaxRows)
            {
                var row = new object?[reader.FieldCount];
                for (var i = 0; i < row.Length; i++)
                {
                    row[i] = Cells.Normalize(reader.IsDBNull(i) ? null : reader.GetValue(i));
                }
                rows.Add(row);
            }
            hasRow = await reader.ReadAsync(ct);
        }

        return new AnalyticResult
        {
            AnalyticId = id,
            Engine = "duckdb",
            Sql = sql,
            Columns = columns,
            Rows = rows,
            RowCount = count,
            Truncated = count > rows.Count,
            ExecuteMs = executeMs,
            ReadRowsMs = clock.Elapsed.TotalMilliseconds - executeMs,
        };
    }

    // Embedded as "Analytics.<id>.duckdb.sql" (see the .csproj).
    private static Dictionary<string, string> LoadSql()
    {
        var assembly = Assembly.GetExecutingAssembly();
        var result = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var name in assembly.GetManifestResourceNames().Where(n => n.StartsWith("Analytics.", StringComparison.Ordinal)))
        {
            using var reader = new StreamReader(assembly.GetManifestResourceStream(name)!);
            result[name["Analytics.".Length..^".duckdb.sql".Length]] = reader.ReadToEnd();
        }
        return result;
    }
}
