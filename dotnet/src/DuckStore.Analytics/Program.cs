using Dapper;
using DuckDB.NET.Data;
using DuckStore.Analytics.Analytics;
using DuckStore.Analytics.Api;
using DuckStore.Analytics.Etl;
using DuckStore.Analytics.Warehouse;
using Scalar.AspNetCore;

// The analytics service: owns the DuckDB warehouse file and the ETL, and answers
// analytics questions over HTTP. It is the only process that writes the file.
var builder = WebApplication.CreateBuilder(args);

builder.Services.AddSingleton<WarehouseSettings>();
builder.Services.AddSingleton<DuckDbWarehouse>();
builder.Services.AddSingleton<ReportService>();
builder.Services.AddSingleton<AnalyticsRunner>();
builder.Services.AddSingleton<WarehouseBuilder>();
builder.Services.AddSingleton<StarToPostgres>();
builder.Services.AddSingleton<EtlService>();
builder.Services.AddHostedService<EtlSchedule>();

builder.Services.AddOpenApi();
builder.Services.AddProblemDetails();
builder.Services.AddExceptionHandler<ErrorHandler>();

var app = builder.Build();

switch (args)
{
    // `dotnet DuckStore.Analytics.dll etl [--incremental] [--target /path/warehouse.duckdb]`: build or update
    // the warehouse once and exit. --target writes somewhere else (for example to compare a full build with
    // the incrementally updated warehouse).
    case ["etl", .. var etlArgs]:
    {
        var warehouseBuilder = app.Services.GetRequiredService<WarehouseBuilder>();
        var mode = etlArgs.Contains("--incremental") ? EtlMode.Incremental : EtlMode.Full;
        var targetIndex = Array.IndexOf(etlArgs, "--target");
        if (targetIndex >= 0)
        {
            var path = Path.GetFullPath(etlArgs[targetIndex + 1]);
            await warehouseBuilder.BuildAsync(new WarehouseTarget(path, Path.Combine(Path.GetDirectoryName(path)!, Path.GetFileNameWithoutExtension(path) + "-parquet")), mode);
        }
        else
        {
            await warehouseBuilder.BuildAsync(mode);
        }
        return;
    }

    // `dotnet DuckStore.Analytics.dll export-sql --out dir`: write the Compare questions as ready-to-run
    // .sql files per engine and data model, for the benchmark scripts in bench/.
    case ["export-sql", .. var exportArgs]:
        Environment.ExitCode = await SqlExporter.RunAsync(
            app.Services.GetRequiredService<DuckDbWarehouse>(), exportArgs, app.Logger);
        return;

    // `dotnet DuckStore.Analytics.dll compare-warehouses a.duckdb b.duckdb`: do two warehouse files hold the same data?
    case ["compare-warehouses", var a, var b]:
        Environment.ExitCode = await WarehouseComparer.RunAsync(a, b) ? 0 : 1;
        return;

    // `dotnet DuckStore.Analytics.dll star-to-postgres`: copy the warehouse's star schema into
    // Postgres (schema dw) for the Compare page's "Postgres, star schema" approach, and exit.
    case ["star-to-postgres", ..]:
        await app.Services.GetRequiredService<StarToPostgres>().RunAsync();
        return;

    // `dotnet DuckStore.Analytics.dll install-extensions`: download the postgres
    // extension into Warehouse:ExtensionDirectory (run while building the Docker image).
    case ["install-extensions", ..]:
        var settings = app.Services.GetRequiredService<WarehouseSettings>();
        await using (var duck = new DuckDBConnection("Data Source=:memory:"))
        {
            await duck.OpenAsync();
            if (settings.ExtensionDirectory is { } dir)
            {
                await duck.ExecuteAsync($"SET extension_directory = '{dir.Replace("'", "''")}'");
            }
            await duck.ExecuteAsync("INSTALL postgres");
            app.Logger.LogInformation("Installed the postgres extension into {Dir}", settings.ExtensionDirectory ?? "the default folder");
        }
        return;
}

app.UseExceptionHandler();

app.MapOpenApi();
app.MapScalarApiReference();
app.MapGet("/health", () => Results.Ok(new { status = "ok" })).ExcludeFromDescription();
app.MapReportEndpoints();
app.MapEtlEndpoints();
app.MapAnalyticsEndpoints();

app.Run();
