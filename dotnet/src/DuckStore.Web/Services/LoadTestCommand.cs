using System.Globalization;

namespace DuckStore.Web.Services;

// Terminal version of the Load test page:
//
//   dotnet DuckStore.Web.dll loadtest [--rate 100] [--duration 60] [--warmup 5] [--report-users 2]
//                                     [--scenarios none,postgres,duckdb] [--csv samples.csv]
//
// --csv writes every store operation (scenario, operation, second, latency_ms, ok), so DuckDB can
// show how latency changed over time: SELECT scenario, floor(second) AS s, quantile_cont(latency_ms, 0.95) ...
public static class LoadTestCommand
{
    public static async Task RunAsync(LoadTestRunner runner, IReadOnlyList<string> args)
    {
        var options = new LoadTestOptions();
        string? csvPath = null;
        for (var i = 0; i < args.Count; i++)
        {
            int Int() => int.Parse(args[++i], CultureInfo.InvariantCulture);
            switch (args[i])
            {
                case "--rate": options = options with { Rate = Int() }; break;
                case "--duration": options = options with { DurationSeconds = Int() }; break;
                case "--warmup": options = options with { WarmupSeconds = Int() }; break;
                case "--report-users": options = options with { ReportUsers = Int() }; break;
                case "--scenarios":
                    options = options with { Scenarios = args[++i].Split(',').Select(x => Enum.Parse<ReportTarget>(x, ignoreCase: true)).ToList() };
                    break;
                case "--csv": csvPath = args[++i]; break;
                default: throw new ArgumentException($"Unknown option '{args[i]}'.");
            }
        }

        Console.WriteLine($"Store traffic: {options.Rate} operations/s (70% product page, 20% order history, 10% checkout), " +
                          $"{options.DurationSeconds} s measured after {options.WarmupSeconds} s warm-up.");
        Console.WriteLine($"Reports: {options.ReportUsers} users, questions {string.Join(", ", options.QuestionList)}.");
        var lastPrinted = (Scenario: -1, Second: -1);
        var results = await runner.RunAsync(options, p =>
        {
            var second = (int)p.Elapsed / 10 * 10;
            if ((p.ScenarioNumber, second) == lastPrinted) return;
            lastPrinted = (p.ScenarioNumber, second);
            Console.Error.WriteLine($"  {p.Scenario.Name()}: {p.Elapsed:0} of {p.Duration} s, {p.StoreDone:N0} store operations, {p.ReportsDone} reports");
        });

        foreach (var result in results)
        {
            Console.WriteLine();
            Console.WriteLine($"Scenario: {result.Reports.Name()}");
            Console.WriteLine($"  {"operation",-15} {"done",7} {"failed",7} {"per s",7} {"p50 ms",9} {"p95 ms",9} {"p99 ms",9} {"max ms",9}");
            foreach (var s in result.Store.Append(result.ReportStats).Where(s => s.Name != "report" || result.Reports != ReportTarget.None))
            {
                Console.WriteLine($"  {s.Name,-15} {s.Done,7:N0} {s.Failed,7:N0} {s.PerSecond,7:N1} {s.P50,9:N1} {s.P95,9:N1} {s.P99,9:N1} {s.Max,9:N0}");
            }
            if (result.OutOfStock > 0) Console.WriteLine($"  ({result.OutOfStock} checkouts found no stock and rolled back)");
        }

        if (results.Count > 1)
        {
            Console.WriteLine();
            Console.WriteLine("p95 latency of the store, ms:");
            Console.WriteLine($"  {"operation",-15} " + string.Join(" ", results.Select(r => $"{r.Reports.Name(),34}")));
            foreach (var operation in Enum.GetValues<StoreOperation>())
            {
                Console.WriteLine($"  {operation.Name(),-15} " + string.Join(" ", results.Select(r => $"{r.Store[(int)operation].P95,34:N1}")));
            }
        }

        if (csvPath is not null)
        {
            await using var csv = new StreamWriter(csvPath);
            await csv.WriteLineAsync("scenario,operation,second,latency_ms,ok");
            foreach (var result in results)
            {
                foreach (var s in result.Samples)
                {
                    await csv.WriteLineAsync(string.Create(CultureInfo.InvariantCulture,
                        $"{result.Reports.ToString().ToLowerInvariant()},{s.Operation.Name()},{s.StartSecond:F3},{s.LatencyMs:F3},{(s.Ok ? "true" : "false")}"));
                }
            }
        }
    }
}
