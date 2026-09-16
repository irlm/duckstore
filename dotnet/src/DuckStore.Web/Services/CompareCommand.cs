using System.Globalization;
using DuckStore.Contracts;

namespace DuckStore.Web.Services;

// Terminal version of the Compare page:
//
//   dotnet DuckStore.Web.dll compare [--runs 5] [--warmup 1] [--csv results.csv] [id ...]
//
// Prints the median run of each approach. With --csv every run (warm-up runs too) is
// written as one row, so the results can be analyzed with DuckDB:
//   SELECT question, approach, median(total_ms) FROM 'results.csv' WHERE NOT warmup GROUP BY ALL;
public static class CompareCommand
{
    public static async Task RunAsync(ComparisonRunner runner, IReadOnlyList<string> args)
    {
        var options = new MeasureOptions();
        string? csvPath = null;
        var ids = new List<string>();
        for (var i = 0; i < args.Count; i++)
        {
            switch (args[i])
            {
                case "--runs": options = options with { Runs = int.Parse(args[++i], CultureInfo.InvariantCulture) }; break;
                case "--warmup": options = options with { Warmups = int.Parse(args[++i], CultureInfo.InvariantCulture) }; break;
                case "--csv": csvPath = args[++i]; break;
                default: ids.Add(args[i]); break;
            }
        }

        var context = await runner.PrepareAsync();
        Console.WriteLine($"Orders up to id {context.MaxOrderId:N0} (warehouse watermark), lookup customer {context.CustomerId}");
        Console.WriteLine($"Median of {options.Runs} run(s) after {options.Warmups} warm-up run(s). Times in ms; min-max = fastest and slowest run.");
        Console.WriteLine();
        Console.WriteLine($"{"",-28} {"Postgres direct",-47}  {"DuckDB service",-60}");
        Console.WriteLine($"{"question",-28} {"median",10} {"min-max",17} {"db",9} {"network",8}  {"median",9} {"min-max",15} {"db",8} {"http",6} {"json",6} {"KB",6}  result");

        await using var csv = csvPath is null ? null : new StreamWriter(csvPath);
        csv?.WriteLine("question,approach,run,warmup,total_ms,db_ms,network_ms,json_ms,rows,payload_bytes");

        foreach (var analytic in AnalyticCatalog.All.Where(a => ids.Count == 0 || ids.Contains(a.Id)))
        {
            var m = await runner.MeasureAsync(analytic.Id, context, options);
            var (pg, duck) = (m.Postgres!, m.DuckDb!);
            var (p, d) = (pg.Median, duck.Median);
            Console.WriteLine(
                $"{analytic.Id,-28} {Ms(p?.TotalMs),10} {Range(pg),17} {Ms(p?.PhaseMs(PhaseKind.Database)),9} {Ms(p?.PhaseMs(PhaseKind.Transfer)),8}  " +
                $"{Ms(d?.TotalMs),9} {Range(duck),15} {Ms(d?.PhaseMs(PhaseKind.Database)),8} {Ms(d?.PhaseMs(PhaseKind.Transfer)),6} {Ms(d?.PhaseMs(PhaseKind.Json)),6} {(d?.PayloadBytes ?? 0) / 1024.0,6:N0}  " +
                (m.Check is { } check ? (check.Same ? "same" : "DIFFERENT: " + check.Message) : "-"));
            if (pg.Error is not null) Console.WriteLine($"   postgres error: {pg.Error}");
            if (duck.Error is not null) Console.WriteLine($"   duckdb error: {duck.Error}");

            if (csv is not null)
            {
                foreach (var series in new[] { pg, duck })
                {
                    WriteRows(csv, analytic.Id, series.Warmups, warmup: true);
                    WriteRows(csv, analytic.Id, series.Runs, warmup: false);
                }
                await csv.FlushAsync();
            }
        }
    }

    private static void WriteRows(StreamWriter csv, string id, IReadOnlyList<ApproachRun> runs, bool warmup)
    {
        for (var i = 0; i < runs.Count; i++)
        {
            var r = runs[i];
            if (r.Error is not null) continue;
            csv.WriteLine(string.Create(CultureInfo.InvariantCulture,
                $"{id},{r.Approach},{i + 1},{(warmup ? "true" : "false")},{r.TotalMs:F3},{r.PhaseMs(PhaseKind.Database):F3},{r.PhaseMs(PhaseKind.Transfer):F3},{r.PhaseMs(PhaseKind.Json):F3},{r.Result?.RowCount},{r.PayloadBytes}"));
        }
    }

    private static string Ms(double? ms) => ms is { } v ? v.ToString(v < 100 ? "N1" : "N0", CultureInfo.CurrentCulture) : "error";

    private static string Range(ApproachSeries s) =>
        s.Runs.Count < 2 || s.Error is not null ? "" : $"{Ms(s.MinMs)}-{Ms(s.MaxMs)}";
}
