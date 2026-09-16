using System.Diagnostics;
using System.Globalization;
using System.Text.Json;
using DuckStore.Analytics.Analytics;
using DuckStore.Contracts;
using Microsoft.AspNetCore.Http.Json;
using Microsoft.Extensions.Options;

namespace DuckStore.Analytics.Api;

public static class AnalyticsEndpoints
{
    public static void MapAnalyticsEndpoints(this IEndpointRouteBuilder app)
    {
        var analytics = app.MapGroup("/api/analytics").WithTags("Analytics (Compare page)");

        analytics.MapGet("/", () => AnalyticCatalog.All.SelectMany(a => new[] { DataModel.Store, DataModel.Star }
                .Select(model => new AnalyticSql(a.Id, model, AnalyticsRunner.SqlFor(a.Id, model)))))
            .WithSummary("The DuckDB SQL of every Compare question, for both data models");

        // The JSON is serialized here, not by the framework, so the time it takes can be
        // measured and reported to the caller in the standard Server-Timing header:
        //   execute   DuckDB, until the first row is available
        //   read      DuckDB, reading the remaining rows and converting values
        //   serialize turning the result into JSON bytes
        analytics.MapPost("/{id}", async (string id, AnalyticRequest request, AnalyticsRunner runner,
                IOptions<JsonOptions> json, HttpContext http, CancellationToken ct) =>
            {
                var result = await runner.RunAsync(id, request, ct);
                var clock = Stopwatch.StartNew();
                var bytes = JsonSerializer.SerializeToUtf8Bytes(result, json.Value.SerializerOptions);
                var serializeMs = clock.Elapsed.TotalMilliseconds;
                http.Response.Headers["Server-Timing"] = string.Create(CultureInfo.InvariantCulture,
                    $"execute;dur={result.ExecuteMs:F3}, read;dur={result.ReadRowsMs:F3}, serialize;dur={serializeMs:F3}");
                return Results.Bytes(bytes, "application/json");
            })
            .WithSummary("Run one Compare question on DuckDB (Model: Star or Store); timings in the Server-Timing header");

        analytics.MapPost("/{id}/plan", (string id, AnalyticRequest request, bool? analyze, AnalyticsRunner runner, CancellationToken ct) =>
                runner.ExplainAsync(id, request, analyze ?? false, ct))
            .WithSummary("The DuckDB plan of one Compare question: EXPLAIN, or EXPLAIN ANALYZE with analyze=true (runs the query)");
    }
}
