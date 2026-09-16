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

// The ETL: Postgres -> DuckDB warehouse file (+ Parquet), in C#.
//
// The SQL lives in the top-level etl/ folder and is compiled into this assembly
// as embedded resources (see the .csproj). It is the same SQL the Go version runs;
// this class only does what a host language must do:
//
//   1. take a lock so only one ETL builds at a time (compatible with the Go ETL)
//   2. open a NEW DuckDB file (warehouse.duckdb.building), attach Postgres read-only
//   3. read the watermark (max order id) and run the SQL files statement by statement
//   4. export Parquet, write dw.etl_info / dw.etl_steps, CHECKPOINT, close
//   5. rename the new file over the old one (atomic); readers switch on their next query
public sealed class WarehouseBuilder(WarehouseSettings settings, IConfiguration config, ILogger<WarehouseBuilder> log)
{
    private static readonly Regex CreateTable = new(@"^\s*CREATE\s+TABLE\s+([\w.]+)\s+AS\b", RegexOptions.IgnoreCase | RegexOptions.Multiline);

    public Task<EtlResult> BuildAsync(IProgress<EtlStep>? progress = null, CancellationToken ct = default) =>
        BuildAsync(new WarehouseTarget(settings.WarehousePath, settings.ParquetDir), progress, ct);

    public async Task<EtlResult> BuildAsync(WarehouseTarget target, IProgress<EtlStep>? progress = null, CancellationToken ct = default)
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
            long maxOrderId;

            await using (var duck = new DuckDBConnection($"Data Source={building}"))
            {
                await duck.OpenAsync(ct);

                async Task Run(string? label, string sql)
                {
                    var t = Stopwatch.GetTimestamp();
                    long rows = await duck.ExecuteAsync(new CommandDefinition(sql, cancellationToken: ct)); // COPY reports its rows
                    if (label is null) return;

                    if (CreateTable.Match(sql) is { Success: true } m)
                    {
                        // CREATE TABLE AS reports no row count; count(*) comes from table metadata, so it is instant.
                        rows = await duck.ExecuteScalarAsync<long>($"SELECT count(*) FROM {m.Groups[1].Value}");
                    }
                    var step = new EtlStep(steps.Count + 1, label, rows, Stopwatch.GetElapsedTime(t));
                    steps.Add(step);
                    progress?.Report(step);
                    log.LogInformation("ETL step {Number,2} {Label,-50} {Rows,12:N0} rows {Ms,8:N0} ms", step.Number, step.Label, step.Rows, step.Duration.TotalMilliseconds);
                }

                // Setup: UTC session, postgres extension, attach the store read-only.
                await Run(null, "SET TimeZone = 'UTC'");
                if (settings.ExtensionDirectory is { } extensions)
                {
                    // The Docker image installs the extension at build time into this folder.
                    await Run(null, $"SET extension_directory = {Quote(extensions)}");
                }
                await Run(null, "INSTALL postgres");
                await Run(null, "LOAD postgres");
                await Run(null, $"ATTACH {Quote(ToLibpq(config.GetConnectionString("Store")!))} AS pg (TYPE postgres, READ_ONLY)");
                maxOrderId = await duck.ExecuteScalarAsync<long>("SELECT coalesce(max(id), 0) FROM pg.store.orders");
                log.LogInformation("ETL watermark: orders up to id {MaxOrderId:N0}", maxOrderId);

                // The shared SQL files, in name order: 01_extract, 02_dimensions, 03_facts, 04_marts.
                foreach (var (name, script) in SqlFiles())
                {
                    var text = script.Replace("{{MAX_ORDER_ID}}", maxOrderId.ToString(CultureInfo.InvariantCulture));
                    foreach (var statement in SqlScript.Split(text))
                    {
                        try
                        {
                            await Run(statement.Label, statement.Sql);
                        }
                        catch (DuckDBException e)
                        {
                            throw new InvalidOperationException($"{name}: {statement.Label ?? "statement"} failed: {e.Message}", e);
                        }
                    }
                }

                // Parquet: open files any tool can read. fact_sales is split into year=/month= folders.
                Directory.CreateDirectory(parquetBuilding);
                string P(string name) => Quote(Path.Combine(parquetBuilding, name));
                await Run("parquet: fact_sales (partitioned)",
                    $"COPY (SELECT *, year(order_date) AS year, month(order_date) AS month FROM dw.fact_sales) TO {P("fact_sales")} (FORMAT parquet, PARTITION_BY (year, month), COMPRESSION zstd)");
                await Run("parquet: fact_orders", $"COPY dw.fact_orders TO {P("fact_orders.parquet")} (FORMAT parquet, COMPRESSION zstd)");
                await Run("parquet: dim_product", $"COPY dw.dim_product TO {P("dim_product.parquet")} (FORMAT parquet)");
                await Run("parquet: dim_customer", $"COPY dw.dim_customer TO {P("dim_customer.parquet")} (FORMAT parquet)");
                await Run("parquet: dim_category", $"COPY dw.dim_category TO {P("dim_category.parquet")} (FORMAT parquet)");

                // Metadata the reports show ("data as of ...").
                var seconds = Stopwatch.GetElapsedTime(started).TotalSeconds.ToString("F3", CultureInfo.InvariantCulture);
                await Run(null, $"""
                    CREATE TABLE dw.etl_info AS
                    SELECT now()::TIMESTAMP AS built_at, {maxOrderId}::BIGINT AS max_order_id, {seconds} AS build_seconds,
                           (SELECT max(placed_at) FROM dw.fact_orders) AS data_until
                    """);
                await Run(null, "CREATE TABLE dw.etl_steps (step_no INTEGER, label VARCHAR, row_count BIGINT, seconds DOUBLE)");
                foreach (var s in steps)
                {
                    await duck.ExecuteAsync("INSERT INTO dw.etl_steps VALUES ($n, $label, $rows, $seconds)",
                        new { n = s.Number, label = s.Label, rows = s.Rows, seconds = s.Duration.TotalSeconds });
                }
                await Run(null, "DETACH pg");
                await Run(null, "CHECKPOINT");
            } // the DuckDB file is closed here, before the swap

            // Swap. rename() replaces the file atomically; open readers keep the old file until they reopen.
            File.Move(building, target.WarehousePath, overwrite: true);
            if (Directory.Exists(target.ParquetDir)) Directory.Delete(target.ParquetDir, recursive: true);
            Directory.Move(parquetBuilding, target.ParquetDir);

            var result = new EtlResult(maxOrderId, steps, Stopwatch.GetElapsedTime(started), new FileInfo(target.WarehousePath).Length, target.WarehousePath);
            log.LogInformation("ETL done in {Seconds:F1} s: {Megabytes:N0} MB, orders up to id {MaxOrderId:N0}",
                result.Duration.TotalSeconds, result.SizeBytes / 1e6, result.MaxOrderId);
            return result;
        }
    }

    /// <summary>How many labelled steps a run reports: "-- step:" statements plus the Parquet exports.</summary>
    public static int ExpectedSteps() =>
        SqlFiles().Sum(f => SqlScript.Split(f.Sql).Count(s => s.Label is not null)) + ParquetExports;

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
