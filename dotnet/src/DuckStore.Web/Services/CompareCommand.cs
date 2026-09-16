using System.Globalization;
using DuckStore.Contracts;

namespace DuckStore.Web.Services;

// Terminal version of the Compare page:
//
//   dotnet DuckStore.Web.dll compare [--runs 5] [--warmup 1] [--csv results.csv]
//                                    [--approaches postgres-store,duckdb-store,postgres-star,duckdb-star] [id ...]
//
// Prints the median of each approach and the two effects:
//   engine = Postgres ÷ DuckDB on the same data model (same tables, same or equivalent SQL)
//   model  = store tables ÷ star schema on the same engine
// With --csv every run (warm-up runs too) is written as one row, so DuckDB can analyze it:
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
                case "--approaches":
                    options = options with
                    {
                        Approaches = args[++i].Split(',').Select(slug => Approaches.All.FirstOrDefault(a => a.Slug == slug)?.Id
                            ?? throw new ArgumentException($"Unknown approach '{slug}'. Use: {string.Join(", ", Approaches.All.Select(a => a.Slug))}")).ToList(),
                    };
                    break;
                default: ids.Add(args[i]); break;
            }
        }

        var context = await runner.PrepareAsync();
        Console.WriteLine($"Orders up to id {context.MaxOrderId:N0} (warehouse watermark), lookup customer {context.CustomerId}");
        Console.WriteLine($"Median of {options.Runs} run(s) after {options.Warmups} warm-up run(s), in ms. Engine = Postgres ÷ DuckDB, model = store ÷ star.");
        foreach (var approach in Enum.GetValues<Approach>())
        {
            if (context.Unavailable(approach) is { } why) Console.WriteLine($"Skipped {approach.Info().Name}: {why}");
        }
        Console.WriteLine();
        Console.WriteLine($"{"question",-28} {"pg store",10} {"duck store",10} {"pg star",10} {"duck star",10}  {"engine:store",12} {"engine:star",11} {"model:pg",9} {"model:duck",10}  result");

        await using var csv = csvPath is null ? null : new StreamWriter(csvPath);
        csv?.WriteLine("question,approach,run,warmup,total_ms,db_ms,network_ms,json_ms,rows,payload_bytes");

        foreach (var analytic in AnalyticCatalog.All.Where(a => ids.Count == 0 || ids.Contains(a.Id)))
        {
            var m = await runner.MeasureAsync(analytic.Id, context, options);
            string Median(Approach a) => m[a] is { } s ? s.Median is { } run ? Ms(run.TotalMs) : "error" : "-";
            string Times(double? x) => x is { } v ? v.ToString(v < 10 ? "0.0" : "N0", CultureInfo.CurrentCulture) + "×" : "-";
            Console.WriteLine(
                $"{analytic.Id,-28} {Median(Approach.PostgresStore),10} {Median(Approach.DuckDbStore),10} {Median(Approach.PostgresStar),10} {Median(Approach.DuckDbStar),10}  " +
                $"{Times(m.Speedup(Approach.PostgresStore, Approach.DuckDbStore)),12} {Times(m.Speedup(Approach.PostgresStar, Approach.DuckDbStar)),11} " +
                $"{Times(m.Speedup(Approach.PostgresStore, Approach.PostgresStar)),9} {Times(m.Speedup(Approach.DuckDbStore, Approach.DuckDbStar)),10}  " +
                (m.Check is { } check ? (check.Same ? "same" : "DIFFERENT: " + check.Message) : "-"));
            foreach (var series in m.Series.Where(s => s.Error is not null))
            {
                Console.WriteLine($"   {series.Approach.Info().Name} error: {series.Error}");
            }

            if (csv is not null)
            {
                foreach (var series in m.Series)
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
                $"{id},{r.Approach.Info().Slug},{i + 1},{(warmup ? "true" : "false")},{r.TotalMs:F3},{r.PhaseMs(PhaseKind.Database):F3},{r.PhaseMs(PhaseKind.Transfer):F3},{r.PhaseMs(PhaseKind.Json):F3},{r.Result?.RowCount},{r.PayloadBytes}"));
        }
    }

    private static string Ms(double ms) => ms.ToString(ms < 100 ? "N1" : "N0", CultureInfo.CurrentCulture);
}
