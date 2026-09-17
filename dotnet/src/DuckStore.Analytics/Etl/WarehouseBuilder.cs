using System.Diagnostics;
using System.Globalization;
using System.Reflection;
using System.Text.RegularExpressions;
using Dapper;
using DuckDB.NET.Data;
using DuckStore.Analytics.Warehouse;
using DuckStore.Contracts;
using Npgsql;

namespace DuckStore.Analytics.Etl;

/// <summary>Where the ETL writes. Tests pass temporary paths; the app uses <see cref="WarehouseSettings"/>.</summary>
public sealed record WarehouseTarget(string WarehousePath, string ParquetDir);

public sealed class EtlAlreadyRunningException() : Exception("Another ETL run is already building the warehouse.");

public enum EtlMode
{
    /// <summary>Copy everything from Postgres into a new warehouse file.</summary>
    Full,

    /// <summary>Copy the current file and apply only what changed since the last run.</summary>
    Incremental,
}

// The ETL: Postgres -> DuckDB warehouse file (+ Parquet), in C#.
//
// The SQL lives in the top-level etl/ folder and is compiled into this assembly
// as embedded resources (see the .csproj). It is the same SQL the Go version runs;
// this class only does what a host language must do:
//
//   1. take a lock so only one ETL builds at a time (compatible with the Go ETL)
//   2. open a NEW DuckDB file (warehouse.duckdb.building), attach Postgres read-only
//      Incremental: the new file starts as a reflink copy of the current warehouse
//   3. read the watermark (max order id) and run the SQL files statement by statement
//      Incremental: etl/incremental/incremental.sql, which reuses steps of the full ETL
//   4. full: export Parquet; both: write dw.etl_info / etl_steps / etl_runs, CHECKPOINT, close
//   5. rename the new file over the old one (atomic); readers switch on their next query
public sealed partial class WarehouseBuilder(WarehouseSettings settings, IConfiguration config, ILogger<WarehouseBuilder> log)
{
    [GeneratedRegex(@"^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:TEMP\s+|TEMPORARY\s+)?TABLE\s+([\w.]+)\s+AS\b", RegexOptions.IgnoreCase | RegexOptions.Multiline)]
    private static partial Regex CreateTable();

    // "-- @full: <name>" in incremental.sql: run these steps of the full ETL here.
    [GeneratedRegex(@"^-- @full: (.+?)\s*$", RegexOptions.Multiline)]
    private static partial Regex FullDirective();

    // The five tables an incremental run changes row by row; everything else in raw.* is copied again.
    private static readonly string[] OrderTables = ["raw.orders", "raw.order_items", "raw.payments", "raw.shipments", "raw.returns"];

    // How far back an incremental run looks for changed orders before the last run's extraction time.
    private static readonly TimeSpan Overlap = TimeSpan.FromMinutes(5);

    public Task<EtlResult> BuildAsync(IProgress<EtlStep>? progress = null, CancellationToken ct = default) =>
        BuildAsync(EtlMode.Full, progress, ct);

    public Task<EtlResult> BuildAsync(EtlMode mode, IProgress<EtlStep>? progress = null, CancellationToken ct = default) =>
        BuildAsync(new WarehouseTarget(settings.WarehousePath, settings.ParquetDir), mode, progress, ct);

    public Task<EtlResult> BuildAsync(WarehouseTarget target, IProgress<EtlStep>? progress = null, CancellationToken ct = default) =>
        BuildAsync(target, EtlMode.Full, progress, ct);

    public async Task<EtlResult> BuildAsync(WarehouseTarget target, EtlMode mode, IProgress<EtlStep>? progress = null, CancellationToken ct = default)
    {
        var started = Stopwatch.GetTimestamp();
        Directory.CreateDirectory(Path.GetDirectoryName(target.WarehousePath)!);

        // FileShare.None takes an exclusive flock() on Linux/macOS, the same lock the Go ETL uses.
        FileStream lockFile;
        try
        {
            lockFile = new FileStream(Path.Combine(Path.GetDirectoryName(target.WarehousePath)!, "etl.lock"),
                FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None);
        }
        catch (IOException)
        {
            throw new EtlAlreadyRunningException();
        }

        await using (lockFile)
        {
            var building = target.WarehousePath + ".building";
            var parquetBuilding = target.ParquetDir + ".building";
            File.Delete(building);
            File.Delete(building + ".wal");
            if (Directory.Exists(parquetBuilding)) Directory.Delete(parquetBuilding, recursive: true);

            var steps = new List<EtlStep>();
            void Record(string label, long rows, TimeSpan duration)
            {
                var step = new EtlStep(steps.Count + 1, label, rows, duration);
                steps.Add(step);
                progress?.Report(step);
                log.LogInformation("ETL step {Number,2} {Label,-50} {Rows,12:N0} rows {Ms,8:N0} ms", step.Number, step.Label, step.Rows, step.Duration.TotalMilliseconds);
            }

            if (mode == EtlMode.Incremental)
            {
                if (!File.Exists(target.WarehousePath))
                {
                    throw new InvalidOperationException("There is no warehouse to update yet. Run a full ETL first.");
                }
                var snapshot = Stopwatch.GetTimestamp();
                await SnapshotAsync(target.WarehousePath, building, ct);
                Record("snapshot of the current warehouse (reflink copy)", 0, Stopwatch.GetElapsedTime(snapshot));
            }

            long maxOrderId;
            long changedOrders;
            await using (var duck = new DuckDBConnection($"Data Source={building}"))
            {
                await duck.OpenAsync(ct);

                // A labelled statement starts a step; the unlabelled statements after it belong to the same step.
                // Rows: what CREATE TABLE AS created, or the rows INSERT/UPDATE/DELETE/COPY changed.
                (string Label, long Rows, long Started)? current = null;
                void EndStep()
                {
                    if (current is { } c) Record(c.Label, c.Rows, Stopwatch.GetElapsedTime(c.Started));
                    current = null;
                }
                async Task Run(string? label, string sql)
                {
                    if (label is not null)
                    {
                        EndStep();
                        current = (label, 0, Stopwatch.GetTimestamp());
                    }
                    long rows = await duck.ExecuteAsync(new CommandDefinition(sql, commandTimeout: 0, cancellationToken: ct)); // COPY reports its rows
                    if (current is not { } c) return;
                    if (CreateTable().Match(sql) is { Success: true } m)
                    {
                        // CREATE TABLE AS reports no row count; count(*) comes from table metadata, so it is instant.
                        rows = await duck.ExecuteScalarAsync<long>($"SELECT count(*) FROM {m.Groups[1].Value}");
                    }
                    current = c with { Rows = c.Rows + Math.Max(rows, 0) };
                }
                async Task RunScript(string name, string script, Func<string, string>? rewrite = null)
                {
                    foreach (var statement in SqlScript.Split(script))
                    {
                        try
                        {
                            await Run(statement.Label, rewrite is null ? statement.Sql : rewrite(statement.Sql));
                        }
                        catch (DuckDBException e)
                        {
                            throw new InvalidOperationException($"{name}: {statement.Label ?? "statement"} failed: {e.Message}", e);
                        }
                    }
                    EndStep();
                }

                // Setup: UTC session, postgres extension, attach the store read-only.
                await Run(null, "SET TimeZone = 'UTC'");
                if (settings.MemoryLimit is { } memory)
                {
                    // Above the limit DuckDB spills to a .tmp folder next to the file instead of failing.
                    await Run(null, $"SET memory_limit = {Quote(memory)}");
                }
                if (settings.ExtensionDirectory is { } extensions)
                {
                    // The Docker image installs the extension at build time into this folder.
                    await Run(null, $"SET extension_directory = {Quote(extensions)}");
                }
                await Run(null, "INSTALL postgres");
                await Run(null, "LOAD postgres");
                await Run(null, $"ATTACH {Quote(ToLibpq(config.GetConnectionString("Store")!))} AS pg (TYPE postgres, READ_ONLY)");

                // Postgres' own clock, read BEFORE the watermark: a change committed after this moment is
                // picked up by the next incremental run.
                var extractedAt = await duck.ExecuteScalarAsync<DateTime>(
                    "SELECT * FROM postgres_query('pg', 'SELECT (now() AT TIME ZONE ''UTC'')::timestamp')");
                maxOrderId = await duck.ExecuteScalarAsync<long>("SELECT coalesce(max(id), 0) FROM pg.store.orders");
                log.LogInformation("ETL ({Mode}) watermark: orders up to id {MaxOrderId:N0}", mode, maxOrderId);

                var fromOrderId = 0L;
                if (mode == EtlMode.Full)
                {
                    // The shared SQL files, in name order: 01_extract, 02_dimensions, 03_facts, 04_marts.
                    foreach (var (name, script) in SqlFiles())
                    {
                        await RunScript(name, script.Replace("{{MAX_ORDER_ID}}", maxOrderId.ToString(CultureInfo.InvariantCulture)));
                    }
                    changedOrders = maxOrderId;

                    // Parquet: open files any tool can read. fact_sales is split into year=/month= folders.
                    Directory.CreateDirectory(parquetBuilding);
                    string P(string name) => Quote(Path.Combine(parquetBuilding, name));
                    await Run("parquet: fact_sales (partitioned)",
                        $"COPY (SELECT *, year(order_date) AS year, month(order_date) AS month FROM dw.fact_sales) TO {P("fact_sales")} (FORMAT parquet, PARTITION_BY (year, month), COMPRESSION zstd)");
                    await Run("parquet: fact_orders", $"COPY dw.fact_orders TO {P("fact_orders.parquet")} (FORMAT parquet, COMPRESSION zstd)");
                    await Run("parquet: dim_product", $"COPY dw.dim_product TO {P("dim_product.parquet")} (FORMAT parquet)");
                    await Run("parquet: dim_customer", $"COPY dw.dim_customer TO {P("dim_customer.parquet")} (FORMAT parquet)");
                    await Run("parquet: dim_category", $"COPY dw.dim_category TO {P("dim_category.parquet")} (FORMAT parquet)");
                    EndStep();
                }
                else
                {
                    fromOrderId = await RunIncrementalAsync(duck, maxOrderId, Run, RunScript, EndStep, ct);
                    changedOrders = await duck.ExecuteScalarAsync<long>("SELECT count(*) FROM changed_orders");
                }

                // Metadata the reports show ("data as of ..."), and what the next incremental run starts from.
                var seconds = Stopwatch.GetElapsedTime(started).TotalSeconds.ToString("F3", CultureInfo.InvariantCulture);
                var modeText = mode == EtlMode.Full ? "full" : "incremental";
                await Run(null, $"""
                    CREATE OR REPLACE TABLE dw.etl_info AS
                    SELECT now()::TIMESTAMP AS built_at, {maxOrderId}::BIGINT AS max_order_id, {seconds} AS build_seconds,
                           (SELECT max(placed_at) FROM dw.fact_orders) AS data_until,
                           '{modeText}' AS mode, TIMESTAMP '{extractedAt:yyyy-MM-dd HH:mm:ss.ffffff}' AS extracted_at
                    """);
                await Run(null, "CREATE OR REPLACE TABLE dw.etl_steps (step_no INTEGER, label VARCHAR, row_count BIGINT, seconds DOUBLE)");
                foreach (var s in steps)
                {
                    await duck.ExecuteAsync("INSERT INTO dw.etl_steps VALUES ($n, $label, $rows, $seconds)",
                        new { n = s.Number, label = s.Label, rows = s.Rows, seconds = s.Duration.TotalSeconds });
                }
                // One row per run, full or incremental: the load history of this warehouse.
                await Run(null, """
                    CREATE TABLE IF NOT EXISTS dw.etl_runs (
                        finished_at TIMESTAMP, mode VARCHAR, from_order_id BIGINT, to_order_id BIGINT,
                        changed_orders BIGINT, seconds DOUBLE)
                    """);
                await duck.ExecuteAsync("INSERT INTO dw.etl_runs VALUES (now()::TIMESTAMP, $mode, $from, $to, $changed, $seconds)",
                    new { mode = modeText, from = fromOrderId, to = maxOrderId, changed = changedOrders, seconds = Stopwatch.GetElapsedTime(started).TotalSeconds });
                await Run(null, "DETACH pg");
                await Run(null, "CHECKPOINT");
            } // the DuckDB file is closed here, before the swap

            // Swap. rename() replaces the file atomically; open readers keep the old file until they reopen.
            File.Move(building, target.WarehousePath, overwrite: true);
            if (mode == EtlMode.Full)
            {
                if (Directory.Exists(target.ParquetDir)) Directory.Delete(target.ParquetDir, recursive: true);
                Directory.Move(parquetBuilding, target.ParquetDir);
            }

            var result = new EtlResult(maxOrderId, steps, Stopwatch.GetElapsedTime(started), new FileInfo(target.WarehousePath).Length, target.WarehousePath);
            log.LogInformation("ETL ({Mode}) done in {Seconds:F1} s: {Megabytes:N0} MB, orders up to id {MaxOrderId:N0}, {Changed:N0} orders loaded",
                mode, result.Duration.TotalSeconds, result.SizeBytes / 1e6, result.MaxOrderId, changedOrders);
            return result;
        }
    }

    // Runs etl/incremental/incremental.sql with its "-- @full:" directives. Returns the previous watermark.
    private static async Task<long> RunIncrementalAsync(DuckDBConnection duck, long maxOrderId,
        Func<string?, string, Task> run, Func<string, string, Func<string, string>?, Task> runScript, Action endStep, CancellationToken ct)
    {
        (long PreviousMaxOrderId, DateTime ExtractedAt) previous;
        try
        {
            previous = await duck.QuerySingleAsync<(long, DateTime)>("SELECT max_order_id, extracted_at FROM dw.etl_info");
        }
        catch (DuckDBException e) when (e.Message.Contains("extracted_at", StringComparison.Ordinal))
        {
            throw new InvalidOperationException(
                "This warehouse was built before incremental loads existed (dw.etl_info has no extracted_at). Run a full ETL once.", e);
        }

        var main = await duck.ExecuteScalarAsync<string>("SELECT current_database()");
        var values = new Dictionary<string, string>
        {
            ["PREVIOUS_MAX_ORDER_ID"] = previous.PreviousMaxOrderId.ToString(CultureInfo.InvariantCulture),
            ["MAX_ORDER_ID"] = maxOrderId.ToString(CultureInfo.InvariantCulture),
            ["CHANGES_SINCE"] = (previous.ExtractedAt - Overlap).ToString("yyyy-MM-dd HH:mm:ss.ffffff", CultureInfo.InvariantCulture),
            ["MAX_RETURN_ID"] = (await duck.ExecuteScalarAsync<long>("SELECT coalesce(max(id), 0) FROM raw.returns")).ToString(CultureInfo.InvariantCulture),
            ["OLD_CHANGED_IDS"] = "{}",
        };
        string Fill(string sql)
        {
            foreach (var (key, value) in values) sql = sql.Replace("{{" + key + "}}", value);
            return sql;
        }

        await run(null, "ATTACH ':memory:' AS delta");
        await run(null, "CREATE SCHEMA delta.raw");
        await run(null, "CREATE SCHEMA delta.dw");

        // The file is split at its "-- @full: <name>" lines: SQL, directive, SQL, directive, ...
        var parts = FullDirective().Split(IncrementalSql());
        for (var i = 0; i < parts.Length; i++)
        {
            if (i % 2 == 0)
            {
                foreach (var statement in SqlScript.Split(parts[i]))
                {
                    await run(statement.Label, Fill(statement.Sql));
                    if (statement.Label == "find new and changed orders")
                    {
                        // The old orders that changed, as a Postgres array literal for the delta queries.
                        values["OLD_CHANGED_IDS"] = await duck.ExecuteScalarAsync<string>(
                            $"SELECT '{{' || coalesce(string_agg(id::VARCHAR, ','), '') || '}}' FROM changed_orders WHERE id <= {previous.PreviousMaxOrderId}") ?? "{}";
                    }
                }
                endStep();
                continue;
            }

            var orReplace = (string sql) => CreateTable().Replace(sql, m => m.Value.Replace("CREATE TABLE", "CREATE OR REPLACE TABLE", StringComparison.OrdinalIgnoreCase));
            switch (parts[i])
            {
                case "reference tables":
                    await runScript("01_extract.sql", FullSteps("01_extract.sql", label => !OrderTables.Any(t => label.StartsWith(t + " ", StringComparison.Ordinal) || label == t)), orReplace);
                    break;
                case "dimensions":
                    await runScript("02_dimensions.sql", FullSteps("02_dimensions.sql", label => !label.StartsWith("dw.dim_product", StringComparison.Ordinal)), orReplace);
                    break;
                case "facts of the changed orders":
                    // In the delta database the full ETL's SQL sees only the changed orders; the tables
                    // it also needs come from the warehouse through views.
                    await run(null, $"CREATE VIEW delta.raw.fx_rates AS FROM \"{main}\".raw.fx_rates");
                    await run(null, $"CREATE VIEW delta.dw.dim_product AS FROM \"{main}\".dw.dim_product");
                    await run(null, "USE delta");
                    await runScript("03_facts.sql (delta)", FullSteps("03_facts.sql", IsOrderFact), sql => sql);
                    await run(null, $"USE \"{main}\"");
                    break;
                case "reviews, inventory and marts":
                    await runScript("03_facts.sql", FullSteps("03_facts.sql", label => !IsOrderFact(label)), orReplace);
                    await runScript("04_marts.sql", FullSteps("04_marts.sql", _ => true), orReplace);
                    break;
                default:
                    throw new InvalidOperationException($"incremental.sql: unknown directive '-- @full: {parts[i]}'.");
            }
        }
        return previous.PreviousMaxOrderId;
    }

    private static bool IsOrderFact(string label) =>
        label.StartsWith("dw.fact_orders", StringComparison.Ordinal) ||
        label.StartsWith("dw.fact_sales", StringComparison.Ordinal) ||
        label.StartsWith("dw.fact_returns", StringComparison.Ordinal);

    // The labelled statements of one full ETL file whose label passes the filter, as one script.
    private static string FullSteps(string file, Func<string, bool> include) =>
        string.Join("\n", SqlScript.Split(SqlFiles().Single(f => f.Name == file).Sql)
            .Where(s => s.Label is not null && include(s.Label))
            .Select(s => $"-- step: {s.Label}\n{s.Sql};\n"));

    /// <summary>
    /// Copies the warehouse for an incremental run. A reflink copy shares the file's blocks until one side
    /// changes (btrfs, XFS, APFS): the 2.6 GB warehouse copies in about 50 ms. Where the file system cannot
    /// do that, cp copies the bytes, which takes seconds.
    /// </summary>
    private static async Task SnapshotAsync(string source, string destination, CancellationToken ct)
    {
        foreach (var (from, to) in new[] { (source, destination), (source + ".wal", destination + ".wal") })
        {
            if (!File.Exists(from)) continue;
            if (!OperatingSystem.IsLinux())
            {
                File.Copy(from, to, overwrite: true);
                continue;
            }
            using var cp = Process.Start(new ProcessStartInfo("cp") { ArgumentList = { "--reflink=auto", from, to }, RedirectStandardError = true })!;
            await cp.WaitForExitAsync(ct);
            if (cp.ExitCode != 0)
            {
                throw new IOException($"Copying {from} failed: {await cp.StandardError.ReadToEndAsync(ct)}");
            }
        }
    }

    /// <summary>How many labelled steps a run reports.</summary>
    public static int ExpectedSteps(EtlMode mode = EtlMode.Full)
    {
        int Labels(string sql) => SqlScript.Split(sql).Count(s => s.Label is not null);
        if (mode == EtlMode.Full)
        {
            return SqlFiles().Sum(f => Labels(f.Sql)) + ParquetExports;
        }
        var parts = FullDirective().Split(IncrementalSql());
        var incremental = parts.Where((_, i) => i % 2 == 0).Sum(Labels);
        var reused = Labels(FullSteps("01_extract.sql", label => !OrderTables.Any(t => label.StartsWith(t + " ", StringComparison.Ordinal) || label == t)))
                     + Labels(FullSteps("02_dimensions.sql", label => !label.StartsWith("dw.dim_product", StringComparison.Ordinal)))
                     + Labels(FullSteps("03_facts.sql", _ => true))
                     + Labels(FullSteps("04_marts.sql", _ => true));
        return 1 + incremental + reused; // + the snapshot
    }

    private const int ParquetExports = 5;

    // The .sql files from the top-level etl/ folder, embedded at build time as "Etl.<name>.sql".
    private static IEnumerable<(string Name, string Sql)> SqlFiles()
    {
        var assembly = Assembly.GetExecutingAssembly();
        var names = assembly.GetManifestResourceNames()
            .Where(n => n.StartsWith("Etl.", StringComparison.Ordinal) && n.EndsWith(".sql", StringComparison.Ordinal))
            .Order(StringComparer.Ordinal)
            .ToList();
        if (names.Count == 0)
        {
            throw new InvalidOperationException("No ETL SQL files are embedded. Check the EmbeddedResource item for etl/*.sql in the .csproj.");
        }
        foreach (var name in names)
        {
            using var reader = new StreamReader(assembly.GetManifestResourceStream(name)!);
            yield return (name["Etl.".Length..], reader.ReadToEnd());
        }
    }

    private static string IncrementalSql()
    {
        using var stream = Assembly.GetExecutingAssembly().GetManifestResourceStream("EtlIncremental.incremental.sql")
            ?? throw new InvalidOperationException("etl/incremental/incremental.sql is not embedded. Check the .csproj.");
        using var reader = new StreamReader(stream);
        return reader.ReadToEnd();
    }

    // DuckDB's postgres extension takes a libpq connection string ("host=... dbname=..."),
    // not the Npgsql format ("Host=...;Database=...").
    internal static string ToLibpq(string npgsqlConnectionString)
    {
        var b = new NpgsqlConnectionStringBuilder(npgsqlConnectionString);
        var parts = new List<string>();
        void Add(string key, object? value)
        {
            var text = value?.ToString();
            if (string.IsNullOrEmpty(text)) return;
            var needsQuotes = text.Any(ch => char.IsWhiteSpace(ch) || ch is '\'' or '\\');
            parts.Add(needsQuotes ? $"{key}='{text.Replace(@"\", @"\\").Replace("'", @"\'")}'" : $"{key}={text}");
        }
        Add("host", b.Host);
        Add("port", b.Port);
        Add("dbname", b.Database);
        Add("user", b.Username);
        Add("password", b.Password);
        Add("sslmode", b.SslMode switch
        {
            SslMode.Disable => "disable",
            SslMode.Require => "require",
            SslMode.VerifyCA => "verify-ca",
            SslMode.VerifyFull => "verify-full",
            _ => "prefer",
        });
        return string.Join(' ', parts);
    }

    private static string Quote(string s) => "'" + s.Replace("'", "''") + "'";
}
