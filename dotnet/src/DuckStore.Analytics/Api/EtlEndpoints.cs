using DuckStore.Analytics.Etl;
using DuckStore.Analytics.Warehouse;

namespace DuckStore.Analytics.Api;

public static class EtlEndpoints
{
    public static void MapEtlEndpoints(this IEndpointRouteBuilder app)
    {
        var etl = app.MapGroup("/api/etl").WithTags("ETL (Postgres → DuckDB)");

        etl.MapPost("/runs", (EtlService service, string? mode) =>
                Results.Accepted("/api/etl/status", service.Start("api",
                    string.Equals(mode, "incremental", StringComparison.OrdinalIgnoreCase) ? EtlMode.Incremental : EtlMode.Full)))
            .WithSummary("Start an ETL run in the background: mode=full (default) or mode=incremental (409 if one is running)");

        etl.MapGet("/history", (ReportService reports, CancellationToken ct) => reports.EtlRunsAsync(ct))
            .WithSummary("Every full and incremental load of the warehouse in use (dw.etl_runs)");

        etl.MapGet("/status", (EtlService service) =>
                service.Current is { } run ? Results.Ok(run) : Results.NoContent())
            .WithSummary("The current or last ETL run of this process, with its steps");
    }
}
