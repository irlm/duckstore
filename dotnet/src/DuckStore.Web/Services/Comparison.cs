using System.Diagnostics;
using System.Globalization;
using System.Net.Http.Headers;
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

/// <summary>The ways to answer a question: three engines × two data models.</summary>
public enum Approach
{
    PostgresStore,
    DuckDbStore,
    PostgresStar,
    DuckDbStar,
    PgDuckDbStore,
    PgDuckDbStar,
}

/// <param name="IsDuckDb">Runs in the analytics service (HTTP), not in Postgres.</param>
/// <param name="PgDuckDb">Runs in Postgres, executed by DuckDB inside Postgres (the pg_duckdb extension).</param>
public sealed record ApproachInfo(Approach Id, string Name, string Slug, bool IsDuckDb, DataModel Model, string Path, bool PgDuckDb = false);

public static class Approaches
{
    public static IReadOnlyList<ApproachInfo> All { get; } =
    [
        new(Approach.PostgresStore, "Postgres · store tables", "postgres-store", false, DataModel.Store, "web → Postgres: store.* tables"),
        new(Approach.DuckDbStore, "DuckDB · store tables", "duckdb-store", true, DataModel.Store, "web → HTTP → analytics service → DuckDB: raw.* copy of the store tables"),
        new(Approach.PostgresStar, "Postgres · star schema", "postgres-star", false, DataModel.Star, "web → Postgres: dw.* copy of the star schema"),
        new(Approach.DuckDbStar, "DuckDB · star schema", "duckdb-star", true, DataModel.Star, "web → HTTP → analytics service → DuckDB: dw.* star schema"),
        new(Approach.PgDuckDbStore, "pg_duckdb · store tables", "pgduckdb-store", false, DataModel.Store, "web → Postgres → DuckDB inside Postgres: store.* tables", PgDuckDb: true),
        new(Approach.PgDuckDbStar, "pg_duckdb · star schema", "pgduckdb-star", false, DataModel.Star, "web → Postgres → DuckDB inside Postgres: dw.* copy of the star schema", PgDuckDb: true),
    ];

    public static ApproachInfo Info(this Approach approach) => All[(int)approach];
}

/// <summary>One run of one approach, with where the time went.</summary>
public sealed record ApproachRun(Approach Approach, AnalyticResult? Result, IReadOnlyList<TimingPhase> Phases, double TotalMs, long? PayloadBytes, string? Error)
{
    public double PhaseMs(PhaseKind kind) => Phases.Where(p => p.Kind == kind).Sum(p => p.Ms);
}

/// <summary>
/// Repeated runs of one approach. One run can be unlucky (a cold cache, another program busy),
/// so the page and the CLI show the median run: half of the runs were faster, half slower.
/// </summary>
public sealed record ApproachSeries(Approach Approach, IReadOnlyList<ApproachRun> Runs, IReadOnlyList<ApproachRun> Warmups)
{
    public string? Error => Warmups.Concat(Runs).FirstOrDefault(r => r.Error is not null)?.Error;

    /// <summary>The middle run by total time (the faster of the two middle runs for an even count).</summary>
    public ApproachRun? Median => Error is null && Runs.Count > 0 ? Runs.OrderBy(r => r.TotalMs).ElementAt((Runs.Count - 1) / 2) : null;

    public double MinMs => Runs.Count > 0 ? Runs.Min(r => r.TotalMs) : 0;
    public double MaxMs => Runs.Count > 0 ? Runs.Max(r => r.TotalMs) : 0;
}

/// <param name="Runs">Timed runs; the median of them is shown.</param>
/// <param name="Warmups">Runs before the timed ones that are not counted: they fill caches and open connections.</param>
/// <param name="Approaches">Which approaches to run; null = all that are available.</param>
/// <param name="Network">Direct connections (null) or through the proxy with a delay (see <see cref="NetworkLab"/>).</param>
public sealed record MeasureOptions(int Runs = 1, int Warmups = 0, IReadOnlyCollection<Approach>? Approaches = null, NetworkSetting? Network = null);

/// <summary>What is running now, for progress text such as "run 2 of 5".</summary>
public sealed record MeasureProgress(Approach Approach, int Run, int Of, bool Warmup);

public sealed record ResultCheck(bool Same, string Message);

public sealed record QuestionMeasurement(string AnalyticId, IReadOnlyList<ApproachSeries> Series, NetworkSetting Network)
{
    public ApproachSeries? this[Approach approach] => Series.FirstOrDefault(s => s.Approach == approach);

    /// <summary>How many times faster <paramref name="faster"/> was than <paramref name="slower"/> (medians).</summary>
    public double? Speedup(Approach slower, Approach faster) =>
        this[slower]?.Median is { TotalMs: > 0 } s && this[faster]?.Median is { TotalMs: > 0 } f ? s.TotalMs / f.TotalMs : null;

    /// <summary>Every result is compared with the first one (in the order of <see cref="Approach"/>).</summary>
    public ResultCheck? Check
    {
        get
        {
            var results = Series.Where(s => s.Median?.Result is not null).Select(s => (s.Approach, Result: s.Median!.Result!)).ToList();
            if (results.Count < 2) return null;
            var (firstApproach, first) = results[0];
            foreach (var (approach, result) in results.Skip(1))
            {
                var check = ComparisonRunner.Check(firstApproach.Info().Name, first, approach.Info().Name, result);
                if (!check.Same) return check;
            }
            return new(true, $"All {results.Count} results are the same: {first.RowCount:N0} rows × {first.Columns.Count} columns" +
                (first.Truncated ? $" (first {first.Rows.Count:N0} compared)" : "") + ".");
        }
    }
}

/// <param name="MaxOrderId">The warehouse watermark, so every approach counts the same orders.</param>
/// <param name="PostgresStarMaxOrderId">The watermark of the star schema copy in Postgres (null = no copy).</param>
/// <param name="PostgresAnalyticsIndexes">How many indexes of analytics/postgres-indexes.sql exist on the store tables.</param>
/// <param name="PgDuckDbVersion">The version of the pg_duckdb extension in Postgres (null = not installed).</param>
public sealed record ComparisonContext(long MaxOrderId, long CustomerId, WarehouseInfo? Warehouse, long? PostgresStarMaxOrderId, int PostgresAnalyticsIndexes, string? PgDuckDbVersion = null)
{
    public string IndexesText => PostgresAnalyticsIndexes == 0
        ? "Postgres store tables without the analytics indexes (make docker-indexes)"
        : $"Postgres store tables with {PostgresAnalyticsIndexes} analytics indexes (make docker-indexes-drop)";

    public bool PostgresStarReady => PostgresStarMaxOrderId == MaxOrderId;

    public string? Unavailable(Approach approach) => approach switch
    {
        Approach.DuckDbStore or Approach.DuckDbStar when Warehouse is null => "The analytics service is not reachable.",
        Approach.PgDuckDbStore or Approach.PgDuckDbStar when PgDuckDbVersion is null =>
            "The pg_duckdb extension is not enabled in Postgres. Run `make docker-pgduckdb`.",
        Approach.PostgresStar or Approach.PgDuckDbStar when PostgresStarMaxOrderId is null =>
            "Postgres has no copy of the star schema yet. Run `make docker-star` (or `dotnet DuckStore.Analytics.dll star-to-postgres`).",
        Approach.PostgresStar or Approach.PgDuckDbStar when !PostgresStarReady =>
            $"The star schema copy in Postgres has orders up to #{PostgresStarMaxOrderId:N0}, the warehouse up to #{MaxOrderId:N0}. Run `make docker-star` again.",
        _ => null,
    };
}

// Runs a Compare question in up to four ways and checks that all give the same answer:
//   Postgres · store tables:  web app --(Postgres protocol)--> Postgres, store.*
//   DuckDB · store tables:    web app --(HTTP + JSON)--> analytics service --> DuckDB, raw.* (same SQL text)
//   Postgres · star schema:   web app --(Postgres protocol)--> Postgres, dw.* (copied from the warehouse)
//   DuckDB · star schema:     web app --(HTTP + JSON)--> analytics service --> DuckDB, dw.*
public sealed class ComparisonRunner(NpgsqlDataSource postgres, AnalyticsApiClient analytics, NetworkLab networkLab, IConfiguration config)
{
    private static readonly AnalyticSqlLibrary Library = new(typeof(ComparisonRunner).Assembly);

    // Connections for the pg_duckdb approaches: every query on them runs in DuckDB inside Postgres.
    // See analytics/postgres-pg_duckdb.sql for the settings.
    private const string PgDuckDbOptions =
        "-c duckdb.force_execution=true -c duckdb.threads_for_postgres_scan=8 -c duckdb.max_workers_per_postgres_scan=8";

    private readonly Lazy<NpgsqlDataSource> _pgDuckDb = new(() =>
        NpgsqlDataSource.Create(new NpgsqlConnectionStringBuilder(config.GetConnectionString("Store")) { Options = PgDuckDbOptions }.ConnectionString));

    private readonly Lazy<NpgsqlDataSource?> _pgDuckDbViaProxy = new(() =>
        config.GetConnectionString("StoreViaProxy") is { Length: > 0 } cs
            ? NpgsqlDataSource.Create(new NpgsqlConnectionStringBuilder(cs) { Options = PgDuckDbOptions }.ConnectionString)
            : null);

    private NpgsqlDataSource PostgresFor(ApproachInfo info, NetworkSetting network) => (info.PgDuckDb, network.ViaProxy) switch
    {
        (false, false) => postgres,
        (false, true) => networkLab.PostgresViaProxy,
        (true, false) => _pgDuckDb.Value,
        (true, true) => _pgDuckDbViaProxy.Value ?? throw new InvalidOperationException("ConnectionStrings:StoreViaProxy is not set."),
    };

    public static string PostgresSqlFor(string id, DataModel model) => Library.For(id, model, "postgres");

    /// <summary>The watermark, a customer for the lookup, and whether Postgres has a current star schema copy.</summary>
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

        long? starMaxOrderId = null;
        await using (var star = postgres.CreateCommand("SELECT max_order_id FROM dw.etl_info"))
        {
            try
            {
                starMaxOrderId = await star.ExecuteScalarAsync(ct) as long?;
            }
            catch (PostgresException e) when (e.SqlState == PostgresErrorCodes.UndefinedTable)
            {
                // no copy of the star schema in Postgres
            }
        }
        await using var indexes = postgres.CreateCommand("SELECT count(*)::int FROM pg_indexes WHERE schemaname = 'store' AND indexname LIKE 'analytics\\_%'");
        var analyticsIndexes = (int)(await indexes.ExecuteScalarAsync(ct))!;

        await using var extension = postgres.CreateCommand("SELECT extversion FROM pg_extension WHERE extname = 'pg_duckdb'");
        var pgDuckDbVersion = await extension.ExecuteScalarAsync(ct) as string;

        return new ComparisonContext(maxOrderId, customerId, warehouse, starMaxOrderId, analyticsIndexes, pgDuckDbVersion);
    }

    /// <summary>
    /// Runs one question with warm-up and timed runs. The approaches take turns
    /// (1, 2, 3, 4, 1, 2, 3, 4, ...) so a slow moment on the machine hits all of
    /// them, not only the one that happened to run at that time.
    /// </summary>
    public async Task<QuestionMeasurement> MeasureAsync(string id, ComparisonContext context, MeasureOptions options,
        Action<MeasureProgress>? progress = null, CancellationToken ct = default)
    {
        var chosen = Enum.GetValues<Approach>()
            .Where(a => options.Approaches is null || options.Approaches.Contains(a))
            .Where(a => context.Unavailable(a) is null)
            .Select(a => (Approach: a, Warmups: new List<ApproachRun>(), Runs: new List<ApproachRun>()))
            .ToList();

        var network = options.Network ?? NetworkSetting.Direct;
        await networkLab.ApplyAsync(network, ct);

        var total = options.Warmups + Math.Max(1, options.Runs);
        for (var i = 0; i < total; i++)
        {
            var warmup = i < options.Warmups;
            foreach (var (approach, warmups, runs) in chosen)
            {
                if (warmups.Concat(runs).Any(r => r.Error is not null)) continue; // failed once: do not repeat
                progress?.Invoke(new MeasureProgress(approach, warmup ? i + 1 : i - options.Warmups + 1, warmup ? options.Warmups : total - options.Warmups, warmup));
                // Clean up the garbage of the previous run first (not timed), so a .NET garbage collection it
                // caused does not land in this run: reading 40k rows took 34-242 ms without this.
                GC.Collect();
                GC.WaitForPendingFinalizers();
                (warmup ? warmups : runs).Add(await RunAsync(approach, id, context, network, ct));
            }
        }
        return new QuestionMeasurement(id, chosen.Select(c => new ApproachSeries(c.Approach, c.Runs, c.Warmups)).ToList(), network);
    }

    public Task<ApproachRun> RunAsync(Approach approach, string id, ComparisonContext context, NetworkSetting network, CancellationToken ct = default) =>
        approach.Info() is { IsDuckDb: true } info
            ? RunDuckDbAsync(approach, id, info.Model, context, network, ct)
            : RunPostgresAsync(approach, id, approach.Info().Model, context, network, ct);


    private async Task<ApproachRun> RunPostgresAsync(Approach approach, string id, DataModel model, ComparisonContext context, NetworkSetting network, CancellationToken ct)
    {
        var sql = PostgresSqlFor(id, model);
        try
        {
            var source = PostgresFor(approach.Info(), network);
            await using var connection = await source.OpenConnectionAsync(ct); // pooled; not timed
            await using var command = new NpgsqlCommand(sql, connection) { CommandTimeout = 1800 };
            AddParameters(command, sql, context);

            // The client cannot see how long Postgres itself worked. A trivial query on the same
            // connection measures one round trip; the fastest of two is taken as the network part
            // of "until the first row". Not included in the total.
            var roundTripMs = double.MaxValue;
            for (var ping = 0; ping < 2; ping++)
            {
                await using var select1 = new NpgsqlCommand("SELECT 1", connection);
                var pingClock = Stopwatch.StartNew();
                await select1.ExecuteScalarAsync(ct);
                roundTripMs = Math.Min(roundTripMs, pingClock.Elapsed.TotalMilliseconds);
            }

            var clock = Stopwatch.StartNew();
            await using var reader = await command.ExecuteReaderAsync(ct);
            var hasRow = await reader.ReadAsync(ct);
            var executeMs = clock.Elapsed.TotalMilliseconds;
            var roundTrip = Math.Min(roundTripMs, executeMs);

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
            return new ApproachRun(approach, result,
            [
                new("Round trip to Postgres (measured with SELECT 1)", roundTrip, PhaseKind.Transfer),
                new(approach.Info().PgDuckDb ? "DuckDB inside Postgres executes (until the first row)" : "Postgres executes (until the first row)", executeMs - roundTrip, PhaseKind.Database),
                new("Rows over the network + decoding", totalMs - executeMs, PhaseKind.Transfer),
            ], totalMs, null, null);
        }
        catch (Exception e) when (e is NpgsqlException or InvalidOperationException && !ct.IsCancellationRequested)
        {
            return new ApproachRun(approach, null, [], 0, null, e.Message);
        }
    }

    private async Task<ApproachRun> RunDuckDbAsync(Approach approach, string id, DataModel model, ComparisonContext context, NetworkSetting network, CancellationToken ct)
    {
        try
        {
            var client = network.ViaProxy ? networkLab.AnalyticsViaProxy() : analytics;
            var clock = Stopwatch.StartNew();
            using var response = await client.PostAnalyticAsync(id, Request(context, model), ct);
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
            return new ApproachRun(approach, result,
            [
                new("DuckDB executes (until the first row)", execute, PhaseKind.Database),
                new("DuckDB reads the remaining rows", read, PhaseKind.Database),
                new("JSON serialize (analytics service)", serialize, PhaseKind.Json),
                new("HTTP + network (request and response)", Math.Max(0, httpMs - execute - read - serialize), PhaseKind.Transfer),
                new("JSON parse (web app)", parseMs, PhaseKind.Json),
            ], clock.Elapsed.TotalMilliseconds, bytes.LongLength, null);
        }
        catch (Exception e) when (e is HttpRequestException or AnalyticsServiceException or JsonException && !ct.IsCancellationRequested)
        {
            return new ApproachRun(approach, null, [], 0, null, e.Message);
        }
    }

    // EXPLAIN shows the plan the engine chose. On Postgres, ANALYZE also runs the query and adds the
    // actual rows and times; BUFFERS adds the pages read from memory (hit) and from disk (read);
    // SETTINGS lists the planner settings that differ from the defaults.
    public async Task<AnalyticPlan> ExplainAsync(Approach approach, string id, ComparisonContext context, bool analyze, CancellationToken ct = default)
    {
        var info = approach.Info();
        if (info.IsDuckDb)
        {
            return await analytics.PlanAsync(id, Request(context, info.Model), analyze, ct);
        }

        var sql = PostgresSqlFor(id, info.Model);
        await using var connection = await PostgresFor(info, NetworkSetting.Direct).OpenConnectionAsync(ct);
        // pg_duckdb supports plain EXPLAIN and EXPLAIN ANALYZE and returns DuckDB's plan.
        var explain = (info.PgDuckDb, analyze) switch
        {
            (true, true) => "EXPLAIN ANALYZE\n",
            (true, false) => "EXPLAIN\n",
            (false, true) => "EXPLAIN (ANALYZE, BUFFERS, SETTINGS)\n",
            (false, false) => "EXPLAIN (SETTINGS)\n",
        };
        await using var command = new NpgsqlCommand(explain + sql, connection)
        {
            CommandTimeout = 1800,
        };
        AddParameters(command, sql, context);

        var clock = Stopwatch.StartNew();
        var lines = new List<string>();
        await using var reader = await command.ExecuteReaderAsync(ct);
        while (await reader.ReadAsync(ct))
        {
            lines.Add(reader.GetString(0));
        }
        return new AnalyticPlan(id, info.PgDuckDb ? "pg_duckdb" : "postgres", analyze, string.Join('\n', lines), clock.Elapsed.TotalMilliseconds);
    }

    private static AnalyticRequest Request(ComparisonContext context, DataModel model) => new(context.MaxOrderId, context.CustomerId, model);

    private static void AddParameters(NpgsqlCommand command, string sql, ComparisonContext context)
    {
        if (sql.Contains("@max_order_id", StringComparison.Ordinal)) command.Parameters.AddWithValue("max_order_id", context.MaxOrderId);
        if (sql.Contains("@customer_id", StringComparison.Ordinal)) command.Parameters.AddWithValue("customer_id", context.CustomerId);
    }

    public static ResultCheck Check(string nameA, AnalyticResult a, string nameB, AnalyticResult b)
    {
        if (a.Columns.Count != b.Columns.Count)
        {
            return new(false, $"Different columns: {nameA} {a.Columns.Count}, {nameB} {b.Columns.Count}.");
        }
        if (a.RowCount != b.RowCount)
        {
            return new(false, $"Different row counts: {nameA} {a.RowCount:N0}, {nameB} {b.RowCount:N0}.");
        }
        var rows = Math.Min(a.Rows.Count, b.Rows.Count);
        for (var r = 0; r < rows; r++)
        {
            for (var c = 0; c < a.Columns.Count; c++)
            {
                if (!Cells.Same(a.Rows[r][c], b.Rows[r][c]))
                {
                    return new(false, $"Row {r + 1}, column {a.Columns[c]}: {nameA} {Show(a.Rows[r][c])}, {nameB} {Show(b.Rows[r][c])}.");
                }
            }
        }
        return new(true, $"Same {a.RowCount:N0} rows × {a.Columns.Count} columns.");
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
}
