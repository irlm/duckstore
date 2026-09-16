using System.Diagnostics;
using System.Globalization;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Reflection;
using System.Text.Json;
using DuckStore.Contracts;
using Npgsql;

namespace DuckStore.Web.Services;

public enum PhaseKind
{
    Database,   // the engine executes the query and produces rows
    Transfer,   // bytes crossing the network (Postgres wire protocol or HTTP)
    Json,       // turning rows into JSON and back
}

public sealed record TimingPhase(string Name, double Ms, PhaseKind Kind);

/// <summary>One way of answering a question, with where the time went.</summary>
public sealed record ApproachRun(string Approach, AnalyticResult? Result, IReadOnlyList<TimingPhase> Phases, double TotalMs, long? PayloadBytes, string? Error);

public sealed record ResultCheck(bool Same, string Message);

public sealed record ComparisonContext(long MaxOrderId, long CustomerId, WarehouseInfo? Warehouse);

// Runs a Compare question two ways and checks that both give the same answer:
//   Postgres direct:  web app --(Postgres protocol)--> Postgres
//   DuckDB service:   web app --(HTTP + JSON)--> analytics service --> DuckDB file
public sealed class ComparisonRunner(NpgsqlDataSource postgres, AnalyticsApiClient analytics)
{
    public const string PostgresApproach = "Postgres direct";
    public const string DuckDbApproach = "DuckDB service";

    private static readonly Dictionary<string, string> PostgresSql = LoadPostgresSql();

    public static string PostgresSqlFor(string id) => PostgresSql[id];

    /// <summary>The warehouse watermark (so both engines see the same orders) and a customer for the lookup.</summary>
    public async Task<ComparisonContext> PrepareAsync(CancellationToken ct = default)
    {
        WarehouseInfo? warehouse = null;
        long maxOrderId;
        try
        {
            warehouse = await analytics.WarehouseAsync(ct);
            maxOrderId = warehouse.MaxOrderId;
        }
        catch (Exception e) when (e is HttpRequestException or AnalyticsServiceException)
        {
            await using var cmd = postgres.CreateCommand("SELECT coalesce(max(id), 0) FROM store.orders");
            maxOrderId = (long)(await cmd.ExecuteScalarAsync(ct))!;
        }

        await using var pick = postgres.CreateCommand("SELECT customer_id FROM store.orders WHERE id = @id");
        pick.Parameters.AddWithValue("id", Math.Max(1, (long)(Random.Shared.NextDouble() * maxOrderId)));
        var customerId = await pick.ExecuteScalarAsync(ct) as long? ?? 1;
        return new ComparisonContext(maxOrderId, customerId, warehouse);
    }

    public async Task<ApproachRun> RunPostgresAsync(string id, ComparisonContext context, CancellationToken ct = default)
    {
        var sql = PostgresSqlFor(id);
        try
        {
            await using var connection = await postgres.OpenConnectionAsync(ct); // pooled; not timed
            await using var command = new NpgsqlCommand(sql, connection) { CommandTimeout = 1800 };
            if (sql.Contains("@max_order_id", StringComparison.Ordinal)) command.Parameters.AddWithValue("max_order_id", context.MaxOrderId);
            if (sql.Contains("@customer_id", StringComparison.Ordinal)) command.Parameters.AddWithValue("customer_id", context.CustomerId);

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
                if (rows.Count < 250_000)
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
            var totalMs = clock.Elapsed.TotalMilliseconds;

            var result = new AnalyticResult
            {
                AnalyticId = id, Engine = "postgres", Sql = sql, Columns = columns, Rows = rows,
                RowCount = count, Truncated = count > rows.Count, ExecuteMs = executeMs, ReadRowsMs = totalMs - executeMs,
            };
            return new ApproachRun(PostgresApproach, result,
            [
                new("Postgres executes (until the first row)", executeMs, PhaseKind.Database),
                new("Rows over the network + decoding", totalMs - executeMs, PhaseKind.Transfer),
            ], totalMs, null, null);
        }
        catch (Exception e) when (e is NpgsqlException or InvalidOperationException && !ct.IsCancellationRequested)
        {
            return new ApproachRun(PostgresApproach, null, [], 0, null, e.Message);
        }
    }

    public async Task<ApproachRun> RunDuckDbAsync(string id, ComparisonContext context, CancellationToken ct = default)
    {
        try
        {
            var clock = Stopwatch.StartNew();
            using var response = await analytics.PostAnalyticAsync(id, new AnalyticRequest(context.MaxOrderId, context.CustomerId), ct);
            var httpMs = clock.Elapsed.TotalMilliseconds; // the body is fully downloaded at this point
            var bytes = await response.Content.ReadAsByteArrayAsync(ct);

            var parse = Stopwatch.StartNew();
            var result = JsonSerializer.Deserialize<AnalyticResult>(bytes, JsonSerializerOptions.Web)!;
            result = result with { Rows = result.Rows.Select(r => r.Select(c => c is JsonElement e ? Cells.FromJson(e) : c).ToArray()).ToList() };
            var parseMs = parse.Elapsed.TotalMilliseconds;

            var server = ServerTiming(response.Headers);
            var execute = server.GetValueOrDefault("execute", result.ExecuteMs);
            var read = server.GetValueOrDefault("read", result.ReadRowsMs);
            var serialize = server.GetValueOrDefault("serialize");
            return new ApproachRun(DuckDbApproach, result,
            [
                new("DuckDB executes (until the first row)", execute, PhaseKind.Database),
                new("DuckDB reads the remaining rows", read, PhaseKind.Database),
                new("JSON serialize (analytics service)", serialize, PhaseKind.Json),
                new("HTTP + network between containers", Math.Max(0, httpMs - execute - read - serialize), PhaseKind.Transfer),
                new("JSON parse (web app)", parseMs, PhaseKind.Json),
            ], clock.Elapsed.TotalMilliseconds, bytes.LongLength, null);
        }
        catch (Exception e) when (e is HttpRequestException or AnalyticsServiceException or JsonException && !ct.IsCancellationRequested)
        {
            return new ApproachRun(DuckDbApproach, null, [], 0, null, e.Message);
        }
    }

    public static ResultCheck Check(AnalyticResult? a, AnalyticResult? b)
    {
        if (a is null || b is null)
        {
            return new(false, "Run both to compare the results.");
        }
        if (a.Columns.Count != b.Columns.Count)
        {
            return new(false, $"Different columns: {a.Columns.Count} vs {b.Columns.Count}.");
        }
        if (a.RowCount != b.RowCount)
        {
            return new(false, $"Different row counts: Postgres {a.RowCount:N0}, DuckDB {b.RowCount:N0}.");
        }
        var rows = Math.Min(a.Rows.Count, b.Rows.Count);
        for (var r = 0; r < rows; r++)
        {
            for (var c = 0; c < a.Columns.Count; c++)
            {
                if (!Cells.Same(a.Rows[r][c], b.Rows[r][c]))
                {
                    return new(false, $"Row {r + 1}, column {a.Columns[c]}: Postgres {Show(a.Rows[r][c])}, DuckDB {Show(b.Rows[r][c])}.");
                }
            }
        }
        return new(true, $"Same {a.RowCount:N0} rows × {a.Columns.Count} columns" + (a.Truncated ? $" (first {rows:N0} compared)" : "") + ".");
    }

    private static string Show(object? v) => v is null ? "NULL" : Convert.ToString(v, CultureInfo.InvariantCulture)!;

    // Server-Timing: execute;dur=12.3, read;dur=1.2, serialize;dur=0.4
    private static Dictionary<string, double> ServerTiming(HttpResponseHeaders headers)
    {
        var result = new Dictionary<string, double>();
        if (!headers.TryGetValues("Server-Timing", out var values)) return result;
        foreach (var metric in values.SelectMany(v => v.Split(',')))
        {
            var parts = metric.Split(';', StringSplitOptions.TrimEntries);
            var dur = parts.FirstOrDefault(p => p.StartsWith("dur=", StringComparison.Ordinal));
            if (dur is not null && double.TryParse(dur[4..], NumberStyles.Float, CultureInfo.InvariantCulture, out var ms))
            {
                result[parts[0]] = ms;
            }
        }
        return result;
    }

    // Embedded as "Analytics.<id>.postgres.sql" (see the .csproj).
    private static Dictionary<string, string> LoadPostgresSql()
    {
        var assembly = Assembly.GetExecutingAssembly();
        var result = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var name in assembly.GetManifestResourceNames().Where(n => n.StartsWith("Analytics.", StringComparison.Ordinal)))
        {
            using var reader = new StreamReader(assembly.GetManifestResourceStream(name)!);
            result[name["Analytics.".Length..^".postgres.sql".Length]] = reader.ReadToEnd();
        }
        return result;
    }
}
