using Dapper;
using DuckDB.NET.Data;
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
builder.Services.AddSingleton<WarehouseBuilder>();
builder.Services.AddSingleton<EtlService>();
builder.Services.AddHostedService<EtlSchedule>();

builder.Services.AddOpenApi();
builder.Services.AddProblemDetails();
builder.Services.AddExceptionHandler<ErrorHandler>();

var app = builder.Build();

switch (args)
{
    // `dotnet DuckStore.Analytics.dll etl`: build the warehouse once and exit.
    case ["etl", ..]:
        await app.Services.GetRequiredService<WarehouseBuilder>().BuildAsync();
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

app.Run();
