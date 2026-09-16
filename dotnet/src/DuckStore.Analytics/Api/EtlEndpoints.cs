using DuckStore.Analytics.Etl;

namespace DuckStore.Analytics.Api;

public static class EtlEndpoints
{
    public static void MapEtlEndpoints(this IEndpointRouteBuilder app)
    {
        var etl = app.MapGroup("/api/etl").WithTags("ETL (Postgres → DuckDB)");

        etl.MapPost("/runs", (EtlService service) =>
                Results.Accepted("/api/etl/status", service.Start("api")))
            .WithSummary("Start an ETL run in the background (409 if one is running)");

        etl.MapGet("/status", (EtlService service) =>
                service.Current is { } run ? Results.Ok(run) : Results.NoContent())
            .WithSummary("The current or last ETL run of this process, with its steps");
    }
}
