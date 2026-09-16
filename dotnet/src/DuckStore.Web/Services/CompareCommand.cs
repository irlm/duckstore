using DuckStore.Contracts;

namespace DuckStore.Web.Services;

// Terminal version of the Compare page: `dotnet DuckStore.Web.dll compare [id ...]`.
public static class CompareCommand
{
    public static async Task RunAsync(ComparisonRunner runner, IReadOnlyList<string> ids)
    {
        var context = await runner.PrepareAsync();
        Console.WriteLine($"Orders up to id {context.MaxOrderId:N0} (warehouse watermark), lookup customer {context.CustomerId}");
        Console.WriteLine($"{"question",-30} {"Postgres",12} {"DuckDB svc",12} {"(db",10} {"http",8} {"json)",8} {"KB",8}  result");
        foreach (var analytic in AnalyticCatalog.All.Where(a => ids.Count == 0 || ids.Contains(a.Id)))
        {
            var pg = await runner.RunPostgresAsync(analytic.Id, context);
            var duck = await runner.RunDuckDbAsync(analytic.Id, context);
            var check = ComparisonRunner.Check(pg.Result, duck.Result);
            double Sum(ApproachRun run, PhaseKind kind) => run.Phases.Where(p => p.Kind == kind).Sum(p => p.Ms);
            Console.WriteLine($"{analytic.Id,-30} {Ms(pg),12} {Ms(duck),12} {Sum(duck, PhaseKind.Database),10:N1} {Sum(duck, PhaseKind.Transfer),8:N1} {Sum(duck, PhaseKind.Json),8:N1} {duck.PayloadBytes / 1024.0,8:N0}  {(check.Same ? "same" : "DIFFERENT: " + check.Message)}");
            if (pg.Error is not null) Console.WriteLine($"   postgres error: {pg.Error}");
            if (duck.Error is not null) Console.WriteLine($"   duckdb error: {duck.Error}");
        }
    }

    private static string Ms(ApproachRun run) => run.Error is not null ? "error" : $"{run.TotalMs:N1} ms";
}
