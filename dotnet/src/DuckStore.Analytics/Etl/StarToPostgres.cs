using System.Diagnostics;
using System.Reflection;
using System.Text.RegularExpressions;
using Dapper;
using DuckDB.NET.Data;
using DuckStore.Analytics.Warehouse;
using DuckStore.Contracts;
using Npgsql;

namespace DuckStore.Analytics.Etl;

// Copies the warehouse's star schema into Postgres (schema dw), so the Compare page can run
// the star-schema SQL on both engines. With the store tables on both engines too, the page
// can separate the engine effect from the data model effect.
//
// The SQL is in etl/postgres-star/: Postgres runs 1_before and 3_after (Npgsql), DuckDB runs
// 2_copy with the warehouse and Postgres attached. The copy includes dw.etl_info, so the
// Compare page knows which watermark the Postgres copy belongs to.
public sealed class StarToPostgres(WarehouseSettings settings, IConfiguration config, ILogger<StarToPostgres> log)
{
    private static readonly Regex CopyTable = new(@"CREATE\s+TABLE\s+pg\.(\w+\.\w+)\s+AS\s+FROM\s+(wh\.\w+\.\w+)", RegexOptions.IgnoreCase);

    public async Task<IReadOnlyList<EtlStep>> RunAsync(CancellationToken ct = default)
    {
        if (!File.Exists(settings.WarehousePath))
        {
            throw new WarehouseNotBuiltException(settings.WarehousePath);
        }

        var steps = new List<EtlStep>();
        void Done(string label, long rows, TimeSpan duration)
        {
            var step = new EtlStep(steps.Count + 1, label, rows, duration);
            steps.Add(step);
            log.LogInformation("Star to Postgres step {Number,2} {Label,-45} {Rows,12:N0} rows {Ms,8:N0} ms", step.Number, label, rows, duration.TotalMilliseconds);
        }

        var connectionString = config.GetConnectionString("Store")!;
        await using var postgres = new NpgsqlConnection(connectionString);
        await postgres.OpenAsync(ct);
        await RunPostgresAsync(postgres, "1_before.postgres.sql", Done, ct);

        await using (var duck = new DuckDBConnection("Data Source=:memory:"))
        {
            await duck.OpenAsync(ct);
            await duck.ExecuteAsync("SET TimeZone = 'UTC'");
            if (settings.MemoryLimit is { } memory) await duck.ExecuteAsync($"SET memory_limit = '{memory.Replace("'", "''")}'");
            if (settings.ExtensionDirectory is { } extensions) await duck.ExecuteAsync($"SET extension_directory = '{extensions.Replace("'", "''")}'");
            await duck.ExecuteAsync("INSTALL postgres");
            await duck.ExecuteAsync("LOAD postgres");
            await duck.ExecuteAsync($"ATTACH '{settings.WarehousePath.Replace("'", "''")}' AS wh (READ_ONLY)");
            await duck.ExecuteAsync($"ATTACH '{WarehouseBuilder.ToLibpq(connectionString).Replace("'", "''")}' AS pg (TYPE postgres)");

            foreach (var statement in SqlScript.Split(Sql("2_copy.duckdb.sql")))
            {
                var clock = Stopwatch.StartNew();
                await duck.ExecuteAsync(new CommandDefinition(statement.Sql, commandTimeout: 0, cancellationToken: ct));
                if (statement.Label is null) continue;
                // CREATE TABLE AS reports no row count: count the source, which DuckDB answers from metadata.
                var rows = CopyTable.Match(statement.Sql) is { Success: true } m
                    ? await duck.ExecuteScalarAsync<long>($"SELECT count(*) FROM {m.Groups[2].Value}")
                    : 0;
                Done(statement.Label, rows, clock.Elapsed);
            }
        }

        await RunPostgresAsync(postgres, "3_after.postgres.sql", Done, ct);
        log.LogInformation("Star schema copied to Postgres in {Seconds:F1} s", steps.Sum(s => s.Duration.TotalSeconds));
        return steps;
    }

    // One labelled step can have several statements; its time is the sum of them.
    private static async Task RunPostgresAsync(NpgsqlConnection postgres, string file, Action<string, long, TimeSpan> done, CancellationToken ct)
    {
        string? label = null;
        var clock = new Stopwatch();
        foreach (var statement in SqlScript.Split(Sql(file)))
        {
            if (statement.Label is not null)
            {
                if (label is not null) done(label, 0, clock.Elapsed);
                (label, clock) = (statement.Label, new Stopwatch());
            }
            clock.Start();
            await using var command = new NpgsqlCommand(statement.Sql, postgres) { CommandTimeout = 0 };
            await command.ExecuteNonQueryAsync(ct);
            clock.Stop();
        }
        if (label is not null) done(label, 0, clock.Elapsed);
    }

    private static string Sql(string file)
    {
        var assembly = Assembly.GetExecutingAssembly();
        using var stream = assembly.GetManifestResourceStream("PostgresStar." + file)
            ?? throw new InvalidOperationException($"etl/postgres-star/{file} is not embedded. Check the .csproj.");
        using var reader = new StreamReader(stream);
        return reader.ReadToEnd();
    }
}
