using System.Diagnostics;
using System.Reflection;
using System.Text;
using System.Text.RegularExpressions;
using DuckDB.NET.Data;
using DuckStore.Analytics.Warehouse;
using DuckStore.Contracts;

namespace DuckStore.Analytics.Analytics;

// Runs the DuckDB side of the Compare page, on either data model (see analytics/README.md):
//   star:  analytics/<id>/star.sql (or star.duckdb.sql) on the dw.* star schema
//   store: analytics/<id>/store.sql, the SQL written for Postgres, on the raw.* copy of the store tables
public sealed partial class AnalyticsRunner(DuckDbWarehouse warehouse)
{
    public const int MaxRows = 250_000;

    private static readonly AnalyticSqlLibrary Library = new(typeof(AnalyticsRunner).Assembly);

    public static string SqlFor(string id, DataModel model)
    {
        if (AnalyticCatalog.Find(id) is null) throw new KeyNotFoundException($"Unknown analytic '{id}'.");
        var sql = Library.For(id, model, "duckdb");
        return model == DataModel.Store ? StoreSqlForDuckDb(sql) : sql;
    }

    // The store SQL is written for Postgres. It runs unchanged on DuckDB's raw.* copy of the same
    // tables after two replacements: the schema name, and the parameter prefix (@ in Npgsql, $ in DuckDB).
    // It is a plain text replacement, so the store SQL must not contain "store." inside a string.
    internal static string StoreSqlForDuckDb(string postgresSql) =>
        ParameterPrefix().Replace(StoreSchema().Replace(postgresSql, "raw."), "$$$1");

    [GeneratedRegex(@"(?<![\w.])store\.(?=\w)")]
    private static partial Regex StoreSchema();

    [GeneratedRegex(@"@(max_order_id|customer_id)\b")]
    private static partial Regex ParameterPrefix();

    public async Task<AnalyticResult> RunAsync(string id, AnalyticRequest request, CancellationToken ct)
    {
        var sql = SqlFor(id, request.Model);
        await using var connection = await warehouse.OpenAsync(ct); // not timed, like a pooled Postgres connection
        await using var command = connection.CreateCommand();
        command.CommandText = sql;
        AddParameters(command, sql, request);

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

    // EXPLAIN shows the plan DuckDB chose. EXPLAIN ANALYZE also runs the query and adds
    // the rows and time of every operator. Both return the plan as text in the last column.
    public async Task<AnalyticPlan> ExplainAsync(string id, AnalyticRequest request, bool analyze, CancellationToken ct)
    {
        var sql = SqlFor(id, request.Model);
        await using var connection = await warehouse.OpenAsync(ct);
        await using var command = connection.CreateCommand();
        command.CommandText = (analyze ? "EXPLAIN ANALYZE\n" : "EXPLAIN\n") + sql;
        AddParameters(command, sql, request);

        var clock = Stopwatch.StartNew();
        var text = new StringBuilder();
        await using var reader = await command.ExecuteReaderAsync(ct);
        while (await reader.ReadAsync(ct))
        {
            text.AppendLine(reader.GetString(reader.FieldCount - 1));
        }
        return new AnalyticPlan(id, "duckdb", analyze, text.ToString(), clock.Elapsed.TotalMilliseconds);
    }

    private static void AddParameters(DuckDBCommand command, string sql, AnalyticRequest request)
    {
        if (sql.Contains("$customer_id", StringComparison.Ordinal))
        {
            command.Parameters.Add(new DuckDBParameter("customer_id", request.CustomerId ?? 0));
        }
        if (sql.Contains("$max_order_id", StringComparison.Ordinal))
        {
            command.Parameters.Add(new DuckDBParameter("max_order_id", request.MaxOrderId));
        }
    }
}
