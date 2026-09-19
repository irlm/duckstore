using System.Globalization;
using System.Text;
using System.Text.RegularExpressions;
using DuckStore.Analytics.Warehouse;
using DuckStore.Contracts;

namespace DuckStore.Analytics.Analytics;

// `export-sql`: write the Compare questions as ready-to-run .sql files, one folder per engine and
// data model, with the parameters replaced by literal values.
//
// The bench scripts in bench/ then run those files with each engine's own client (psql, duckdb,
// sqlcmd) and read the time the engine itself reports, the way NetworkLab's duckbench does. The
// rewriting rules stay here, in one place, so a benchmark never runs SQL that differs from what the
// Compare page runs.
public static partial class SqlExporter
{
    public static async Task<int> RunAsync(DuckDbWarehouse warehouse, IReadOnlyList<string> args, ILogger log, CancellationToken ct = default)
    {
        string? outDir = null;
        IReadOnlyList<string> engines = ["postgres", "duckdb"];
        IReadOnlyList<DataModel> models = [DataModel.Store, DataModel.Star];
        IReadOnlyList<string> ids = AnalyticCatalog.All.Select(a => a.Id).ToList();
        long? maxOrderId = null;
        long? customerId = null;

        for (var i = 0; i < args.Count; i++)
        {
            switch (args[i])
            {
                case "--out": outDir = args[++i]; break;
                case "--engines": engines = args[++i].Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries); break;
                case "--models": models = args[++i].Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
                    .Select(m => Enum.Parse<DataModel>(m, ignoreCase: true)).ToList(); break;
                case "--questions": ids = args[++i].Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries); break;
                case "--max-order-id": maxOrderId = long.Parse(args[++i], CultureInfo.InvariantCulture); break;
                case "--customer-id": customerId = long.Parse(args[++i], CultureInfo.InvariantCulture); break;
                case "-h" or "--help": Usage(log); return 0;
                default: log.LogError("Unknown argument '{Arg}'.", args[i]); Usage(log); return 1;
            }
        }

        if (outDir is null)
        {
            log.LogError("--out is required.");
            Usage(log);
            return 1;
        }

        if (ids.FirstOrDefault(id => AnalyticCatalog.Find(id) is null) is { } unknown)
        {
            log.LogError("Unknown question '{Id}'.", unknown);
            return 1;
        }

        // Both parameters default to the warehouse: the watermark every approach answers on, and the
        // customer of that order, so a rerun on the same data exports the same SQL.
        if (maxOrderId is null || customerId is null)
        {
            await using var connection = await warehouse.OpenAsync(ct);
            maxOrderId ??= await Scalar(connection, "SELECT max_order_id FROM dw.etl_info", ct);
            customerId ??= await Scalar(connection, $"SELECT customer_id FROM raw.orders WHERE id = {maxOrderId}", ct);
        }

        if (maxOrderId is null)
        {
            log.LogError("No watermark: pass --max-order-id, or build the warehouse first.");
            return 1;
        }

        var written = 0;
        var missing = new List<string>();
        foreach (var engine in engines)
        {
            foreach (var model in models)
            {
                var dir = Path.Combine(outDir, engine, model.ToString().ToLowerInvariant());
                Directory.CreateDirectory(dir);
                foreach (var id in ids)
                {
                    string sql;
                    try
                    {
                        sql = AnalyticsRunner.SqlFor(id, model, engine);
                    }
                    catch (KeyNotFoundException)
                    {
                        missing.Add($"{engine}/{model.ToString().ToLowerInvariant()}/{id}");
                        continue;
                    }

                    if (AnalyticCatalog.Find(id)!.NeedsCustomer && customerId is null)
                    {
                        log.LogError("Question '{Id}' needs a customer: pass --customer-id.", id);
                        return 1;
                    }

                    await File.WriteAllTextAsync(Path.Combine(dir, id + ".sql"), Substitute(sql, maxOrderId.Value, customerId), ct);
                    written++;
                }
            }
        }

        var parameters = new StringBuilder()
            .AppendLine("# duckstore analytics SQL")
            .AppendLine(CultureInfo.InvariantCulture, $"exported_utc: {DateTime.UtcNow:yyyy-MM-ddTHH:mm:ssZ}")
            .AppendLine(CultureInfo.InvariantCulture, $"max_order_id: {maxOrderId}")
            .AppendLine(CultureInfo.InvariantCulture, $"customer_id: {customerId}")
            .AppendLine(CultureInfo.InvariantCulture, $"engines: {string.Join(',', engines)}")
            .AppendLine(CultureInfo.InvariantCulture, $"models: {string.Join(',', models.Select(m => m.ToString().ToLowerInvariant()))}")
            .AppendLine(CultureInfo.InvariantCulture, $"questions: {written}")
            .ToString();
        await File.WriteAllTextAsync(Path.Combine(outDir, "PARAMS"), parameters, ct);

        foreach (var gap in missing) log.LogWarning("No SQL for {Gap}.", gap);
        log.LogInformation("Wrote {Count} files to {Dir} (watermark {MaxOrderId}, customer {CustomerId}).", written, outDir, maxOrderId, customerId);
        return written > 0 ? 0 : 1;
    }

    // The store SQL uses @max_order_id / @customer_id (Npgsql), the DuckDB version $max_order_id.
    // A command-line client has no parameters, so the benchmark files carry the values as literals.
    internal static string Substitute(string sql, long maxOrderId, long? customerId) =>
        Parameter().Replace(sql, match => match.Groups[1].Value switch
        {
            "max_order_id" => maxOrderId.ToString(CultureInfo.InvariantCulture),
            _ => (customerId ?? 0).ToString(CultureInfo.InvariantCulture),
        });

    [GeneratedRegex(@"[@$](max_order_id|customer_id)\b")]
    private static partial Regex Parameter();

    private static async Task<long?> Scalar(DuckDB.NET.Data.DuckDBConnection connection, string sql, CancellationToken ct)
    {
        try
        {
            await using var command = connection.CreateCommand();
            command.CommandText = sql;
            return await command.ExecuteScalarAsync(ct) as long?;
        }
        catch (Exception)
        {
            return null; // no warehouse yet, or no such table: the caller asks for the flag instead
        }
    }

    private static void Usage(ILogger log) => log.LogInformation(
        """
        Usage: dotnet DuckStore.Analytics.dll export-sql --out DIR [options]
          --out DIR             where to write <engine>/<model>/<question>.sql and PARAMS
          --engines a,b         postgres, duckdb, mssql (default: postgres,duckdb)
          --models store,star   data models to export (default: both)
          --questions a,b       question ids (default: all 15)
          --max-order-id N      the watermark (default: dw.etl_info of the warehouse)
          --customer-id N       customer for the lookup question (default: the watermark order's)
        """);
}
