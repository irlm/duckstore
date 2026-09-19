using System.Collections.Concurrent;
using System.Diagnostics;
using DuckStore.Contracts;
using Npgsql;

namespace DuckStore.Web.Services;

/// <summary>Where the report users of a scenario send their questions.</summary>
public enum ReportTarget
{
    /// <summary>No reports: the store traffic alone, the baseline.</summary>
    None,

    /// <summary>Reports on the same Postgres the store uses (store tables).</summary>
    Postgres,

    /// <summary>Reports through the analytics service (DuckDB, star schema).</summary>
    DuckDb,

    /// <summary>Reports on SQL Server (star schema, clustered columnstore).</summary>
    SqlServer,
}

public enum StoreOperation
{
    ProductPage,
    OrderHistory,
    Checkout,
}

/// <param name="Rate">Store operations started per second, whether the previous ones finished or not.</param>
/// <param name="ReportUsers">People running reports at the same time, each one report after the other.</param>
/// <param name="WarmupSeconds">Not counted: connections open and caches fill.</param>
/// <param name="Rounds">How many times the scenarios are repeated, taking turns. Their samples are
/// added together, so a long measurement is not one scenario running while the machine was cool and
/// another while it was hot — anything that drifts hits every scenario equally.</param>
public sealed record LoadTestOptions(
    int Rate = 100,
    int DurationSeconds = 60,
    int WarmupSeconds = 5,
    int ReportUsers = 2,
    IReadOnlyList<ReportTarget>? Scenarios = null,
    IReadOnlyList<string>? ReportQuestions = null,
    int Rounds = 1)
{
    public static IReadOnlyList<string> DefaultReportQuestions { get; } =
        ["brand-returns", "active-customers", "sales-hierarchy", "unusual-days", "monthly-revenue"];

    public IReadOnlyList<ReportTarget> ScenarioList => Scenarios ?? [ReportTarget.None, ReportTarget.Postgres, ReportTarget.DuckDb];

    public IReadOnlyList<string> QuestionList => ReportQuestions ?? DefaultReportQuestions;
}

/// <summary>Latency percentiles of one kind of operation, in milliseconds.</summary>
/// <param name="P999">The 99.9th percentile; <paramref name="P9999"/> the 99.99th.</param>
/// <param name="P99Low">Ends of a 95% confidence interval for p99, from the order statistics.</param>
/// <param name="AboveP9999">How many samples are above p99.99. Under ~10 the number is a guess, not a measurement.</param>
public sealed record LatencyStats(
    string Name, int Done, int Failed, double PerSecond,
    double P50, double P95, double P99, double P999, double P9999, double Max,
    double P99Low, double P99High, int AboveP9999)
{
    public static LatencyStats From(string name, IReadOnlyCollection<double> ms, int failed, double seconds)
    {
        if (ms.Count == 0) return new(name, 0, failed, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0);
        var sorted = ms.Order().ToArray();
        var n = sorted.Length;
        double Percentile(double p) => sorted[Math.Min(n - 1, (int)Math.Ceiling(p * n) - 1)];

        // A percentile read off a sample is an estimate. Its 95% confidence interval is the pair of
        // order statistics around the expected rank, from the normal approximation of the binomial:
        // rank = n·q ± 1.96·sqrt(n·q·(1-q)). Wide interval = not enough samples for that percentile.
        double At(double rank) => sorted[Math.Clamp((int)Math.Round(rank) - 1, 0, n - 1)];
        var spread99 = 1.96 * Math.Sqrt(n * 0.99 * 0.01);

        return new(name, n, failed, n / seconds,
            Percentile(0.50), Percentile(0.95), Percentile(0.99), Percentile(0.999), Percentile(0.9999), sorted[^1],
            At(n * 0.99 - spread99), At(n * 0.99 + spread99), (int)Math.Floor(n * 0.0001));
    }

    /// <summary>A percentile needs samples above it to mean anything: 10 is the smallest honest number.</summary>
    public bool P9999Supported => AboveP9999 >= 10;
}

public sealed record ScenarioResult(
    ReportTarget Reports,
    IReadOnlyList<LatencyStats> Store,
    LatencyStats ReportStats,
    int OutOfStock,
    double MeasuredSeconds,
    IReadOnlyList<(StoreOperation Operation, double StartSecond, double LatencyMs, bool Ok)> Samples);

public sealed record LoadTestProgress(ReportTarget Scenario, int ScenarioNumber, int ScenarioCount, double Elapsed, int Duration, int StoreDone, int ReportsDone);

public static class LoadTestText
{
    public static string Name(this ReportTarget target) => target switch
    {
        ReportTarget.None => "store only",
        ReportTarget.Postgres => "store + reports on Postgres",
        ReportTarget.SqlServer => "store + reports on SQL Server",
        _ => "store + reports on DuckDB service",
    };

    public static string Name(this StoreOperation operation) => operation switch
    {
        StoreOperation.ProductPage => "product page",
        StoreOperation.OrderHistory => "order history",
        _ => "checkout",
    };
}

// A load test of the store while people run reports.
//
// Store traffic is "open loop": operations START at a fixed rate (100 per second = one every 10 ms),
// whether the database keeps up or not, like customers who do not wait for each other. Latency is
// measured from when an operation was due to start, so time spent waiting for a free connection counts
// too. A closed loop ("next request when the last one finished") would hide a slow database, because a
// slow database would simply receive fewer requests.
//
// Mix: 70% product page (3 reads by key), 20% order history (the lookup of the Compare page),
// 10% checkout (a write transaction: lock stock rows, update stock, insert order, lines, payment, shipment).
//
// Report users run analytics questions one after the other, on Postgres (store tables) or through
// the analytics service (DuckDB, star schema), during the whole scenario.
public sealed class LoadTestRunner(IConfiguration config, ComparisonRunner comparison)
{
    public async Task<IReadOnlyList<ScenarioResult>> RunAsync(LoadTestOptions options, Action<LoadTestProgress>? progress = null, CancellationToken ct = default)
    {
        // The store has its own connection pool, like a separate application: reports (which use the
        // Compare page's pool) cannot take its connections.
        var connectionString = config.GetConnectionString("Store")!;
        await using var store = new NpgsqlDataSourceBuilder(connectionString + ";Maximum Pool Size=64;Application Name=loadtest-store").Build();

        var bounds = await BoundsAsync(store, ct);
        var context = await comparison.PrepareAsync(ct);
        var rounds = new List<ScenarioResult>();
        var number = 0;
        var total = options.ScenarioList.Count * Math.Max(1, options.Rounds);
        for (var round = 0; round < Math.Max(1, options.Rounds); round++)
        {
            foreach (var scenario in options.ScenarioList)
            {
                number++;
                rounds.Add(await RunScenarioAsync(scenario, number, total, options, store, bounds, context, progress, ct));
            }
        }

        // One result per scenario, with the samples of all its rounds behind it.
        return options.ScenarioList
            .Select(scenario => Merge(rounds.Where(r => r.Reports == scenario).ToList(), options))
            .Where(r => r is not null)
            .Select(r => r!)
            .ToList();
    }

    /// <summary>Adds the rounds of one scenario together and recomputes the percentiles over all of them.</summary>
    private static ScenarioResult? Merge(IReadOnlyList<ScenarioResult> rounds, LoadTestOptions options)
    {
        if (rounds.Count == 0) return null;
        if (rounds.Count == 1) return rounds[0];

        var seconds = rounds.Sum(r => r.MeasuredSeconds);
        var samples = rounds.SelectMany(r => r.Samples).ToList();
        var store = Enum.GetValues<StoreOperation>().Select(op => LatencyStats.From(op.Name(),
            samples.Where(s => s.Operation == op && s.Ok).Select(s => s.LatencyMs).ToList(),
            samples.Count(s => s.Operation == op && !s.Ok), seconds)).ToList();

        // The report stats have no per-sample list on the result, so they are pooled from their counts:
        // the percentiles of the longest round are kept and the throughput is recomputed over all rounds.
        var reports = rounds.Select(r => r.ReportStats).ToList();
        var reportStats = reports[0] with
        {
            Done = reports.Sum(r => r.Done),
            Failed = reports.Sum(r => r.Failed),
            PerSecond = reports.Sum(r => r.Done) / seconds,
            P50 = reports.Average(r => r.P50),
            P95 = reports.Average(r => r.P95),
            P99 = reports.Max(r => r.P99),
            Max = reports.Max(r => r.Max),
        };

        return new ScenarioResult(rounds[0].Reports, store, reportStats, rounds.Sum(r => r.OutOfStock), seconds, samples);
    }

    private sealed record Bounds(long MaxProductId, long MaxCustomerId);

    private static async Task<Bounds> BoundsAsync(NpgsqlDataSource store, CancellationToken ct)
    {
        await using var command = store.CreateCommand("SELECT (SELECT max(id) FROM store.products), (SELECT max(id) FROM store.customers)");
        await using var reader = await command.ExecuteReaderAsync(ct);
        await reader.ReadAsync(ct);
        return new Bounds(reader.GetInt64(0), reader.GetInt64(1));
    }

    private async Task<ScenarioResult> RunScenarioAsync(ReportTarget scenario, int number, int scenarioCount, LoadTestOptions options,
        NpgsqlDataSource store, Bounds bounds, ComparisonContext context,
        Action<LoadTestProgress>? progress, CancellationToken ct)
    {
        var samples = new ConcurrentBag<(StoreOperation Operation, double StartSecond, double LatencyMs, bool Ok)>();
        var reportSamples = new ConcurrentBag<(double StartSecond, double Ms, bool Ok)>();
        var outOfStock = 0;
        var total = options.WarmupSeconds + options.DurationSeconds;
        var clock = Stopwatch.StartNew();

        using var stop = CancellationTokenSource.CreateLinkedTokenSource(ct);
        stop.CancelAfter(TimeSpan.FromSeconds(total));

        // Report users start with the scenario and stop at its end (a running report is cancelled).
        var reportTasks = scenario == ReportTarget.None ? Array.Empty<Task>() : Enumerable.Range(0, options.ReportUsers).Select(user => Task.Run(async () =>
        {
            var question = user;
            while (!stop.IsCancellationRequested)
            {
                var id = options.QuestionList[question++ % options.QuestionList.Count];
                var start = clock.Elapsed.TotalSeconds;
                try
                {
                    // The store always runs on Postgres; only the reports move. Postgres answers them
                    // from the store tables it already has, the other two from their own star schema.
                    var approach = scenario switch
                    {
                        ReportTarget.Postgres => Approach.PostgresStore,
                        ReportTarget.SqlServer => Approach.SqlServerStar,
                        _ => Approach.DuckDbStar,
                    };
                    var run = await comparison.RunAsync(approach, id, context, NetworkSetting.Direct, stop.Token);
                    reportSamples.Add((start, run.TotalMs, run.Error is null));
                }
                catch (OperationCanceledException)
                {
                    break;
                }
            }
        })).ToArray();

        // Store traffic: open loop at a fixed rate.
        var running = new List<Task>();
        var interval = TimeSpan.FromSeconds(1.0 / options.Rate);
        using var concurrency = new SemaphoreSlim(64);
        for (long i = 0; ; i++)
        {
            var due = interval * i;
            if (due.TotalSeconds >= total || ct.IsCancellationRequested) break;
            var wait = due - clock.Elapsed;
            if (wait > TimeSpan.Zero) await Task.Delay(wait, CancellationToken.None);

            var operation = (Random.Shared.Next(100)) switch
            {
                < 70 => StoreOperation.ProductPage,
                < 90 => StoreOperation.OrderHistory,
                _ => StoreOperation.Checkout,
            };
            running.Add(Task.Run(async () =>
            {
                var ok = true;
                await concurrency.WaitAsync(CancellationToken.None);
                try
                {
                    var placed = await RunStoreOperationAsync(operation, store, bounds, CancellationToken.None);
                    if (!placed) Interlocked.Increment(ref outOfStock);
                }
                catch (Exception e) when (e is NpgsqlException or InvalidOperationException or TimeoutException)
                {
                    ok = false;
                }
                finally
                {
                    concurrency.Release();
                }
                // Latency counts from the scheduled start, not from when the operation really began,
                // so waiting for a connection counts. Task.Delay can return a fraction of a
                // millisecond early, and a sub-millisecond read then finishes before its slot: that
                // is timer jitter, not negative latency, so it is clamped at zero.
                samples.Add((operation, due.TotalSeconds, Math.Max(0, (clock.Elapsed - due).TotalMilliseconds), ok));
            }));

            if (i % options.Rate == 0)
            {
                progress?.Invoke(new LoadTestProgress(scenario, number, scenarioCount, clock.Elapsed.TotalSeconds, total, samples.Count, reportSamples.Count));
                running.RemoveAll(t => t.IsCompleted);
            }
        }

        await Task.WhenAll(running);
        await Task.WhenAll(reportTasks);

        // Count only what started after the warm-up.
        var measured = samples.Where(s => s.StartSecond >= options.WarmupSeconds).ToList();
        var seconds = (double)options.DurationSeconds;
        var storeStats = Enum.GetValues<StoreOperation>().Select(op => LatencyStats.From(op.Name(),
            measured.Where(s => s.Operation == op && s.Ok).Select(s => s.LatencyMs).ToList(),
            measured.Count(s => s.Operation == op && !s.Ok), seconds)).ToList();
        var reportMeasured = reportSamples.Where(r => r.StartSecond >= options.WarmupSeconds).ToList();
        var reportStats = LatencyStats.From("report", reportMeasured.Where(r => r.Ok).Select(r => r.Ms).ToList(), reportMeasured.Count(r => !r.Ok), seconds);

        progress?.Invoke(new LoadTestProgress(scenario, number, scenarioCount, total, total, samples.Count, reportSamples.Count));
        return new ScenarioResult(scenario, storeStats, reportStats, outOfStock, seconds, measured.OrderBy(s => s.StartSecond).ToList());
    }

    /// <returns>False when a checkout found no stock and rolled back.</returns>
    private static async Task<bool> RunStoreOperationAsync(StoreOperation operation, NpgsqlDataSource store, Bounds bounds, CancellationToken ct)
    {
        await using var connection = await store.OpenConnectionAsync(ct);
        switch (operation)
        {
            case StoreOperation.ProductPage:
                await ProductPageAsync(connection, Random.Shared.NextInt64(1, bounds.MaxProductId + 1), ct);
                return true;
            case StoreOperation.OrderHistory:
                await OrderHistoryAsync(connection, Random.Shared.NextInt64(1, bounds.MaxCustomerId + 1), ct);
                return true;
            default:
                return await CheckoutAsync(connection, bounds, ct);
        }
    }

    // What a product page reads: the product, its stock per warehouse, its rating. Three reads by key.
    private static async Task ProductPageAsync(NpgsqlConnection connection, long productId, CancellationToken ct)
    {
        await using var batch = new NpgsqlBatch(connection)
        {
            BatchCommands =
            {
                new("""
                    SELECT p.name, p.price_usd, b.name AS brand, c.name AS category
                    FROM store.products p
                    JOIN store.brands b ON b.id = p.brand_id
                    JOIN store.categories c ON c.id = p.category_id
                    WHERE p.id = $1
                    """) { Parameters = { new() { Value = productId } } },
                new("""
                    SELECT w.code, i.quantity_on_hand
                    FROM store.inventory i
                    JOIN store.warehouses w ON w.id = i.warehouse_id
                    WHERE i.product_id = $1
                    ORDER BY w.code
                    """) { Parameters = { new() { Value = productId } } },
                new("""
                    SELECT count(*), round(avg(rating), 2)
                    FROM store.reviews
                    WHERE product_id = $1
                    """) { Parameters = { new() { Value = productId } } },
            },
        };
        await using var reader = await batch.ExecuteReaderAsync(ct);
        do
        {
            while (await reader.ReadAsync(ct)) { }
        } while (await reader.NextResultAsync(ct));
    }

    // A customer's 20 latest orders, as the order history page shows them.
    private static async Task OrderHistoryAsync(NpgsqlConnection connection, long customerId, CancellationToken ct)
    {
        await using var command = new NpgsqlCommand("""
            SELECT o.id, o.placed_at, o.status, o.total, o.currency_code
            FROM store.orders o
            WHERE o.customer_id = $1
            ORDER BY o.placed_at DESC, o.id DESC
            LIMIT 20
            """, connection) { Parameters = { new() { Value = customerId } } };
        await using var reader = await command.ExecuteReaderAsync(ct);
        while (await reader.ReadAsync(ct)) { }
    }

    // A checkout: one transaction, like the Go store's (internal/web/checkout.go), without the cart.
    private static async Task<bool> CheckoutAsync(NpgsqlConnection connection, Bounds bounds, CancellationToken ct)
    {
        var customerId = Random.Shared.NextInt64(1, bounds.MaxCustomerId + 1);
        var productIds = Enumerable.Range(0, Random.Shared.Next(1, 4)).Select(_ => Random.Shared.NextInt64(1, bounds.MaxProductId + 1)).Distinct().Order().ToArray();
        var quantities = productIds.Select(_ => Random.Shared.Next(1, 3)).ToArray();

        await using var tx = await connection.BeginTransactionAsync(ct);

        // 1. Lock the stock rows in a fixed order (no deadlocks), and pick a warehouse per line.
        var warehouses = new long[productIds.Length];
        await using (var stock = new NpgsqlCommand("""
            SELECT product_id, warehouse_id, quantity_on_hand
            FROM store.inventory
            WHERE product_id = ANY($1)
            ORDER BY warehouse_id, product_id
            FOR UPDATE
            """, connection, tx) { Parameters = { new() { Value = productIds } } })
        await using (var reader = await stock.ExecuteReaderAsync(ct))
        {
            while (await reader.ReadAsync(ct))
            {
                var line = Array.IndexOf(productIds, reader.GetInt64(0));
                if (warehouses[line] == 0 && reader.GetInt32(2) >= quantities[line]) warehouses[line] = reader.GetInt64(1);
            }
        }
        if (warehouses.Contains(0))
        {
            await tx.RollbackAsync(ct);
            return false;
        }

        // 2. Take the stock for all lines in one statement.
        await using (var update = new NpgsqlCommand("""
            UPDATE store.inventory i
            SET quantity_on_hand = i.quantity_on_hand - x.qty, updated_at = now()
            FROM unnest($1::bigint[], $2::bigint[], $3::int[]) AS x(warehouse_id, product_id, qty)
            WHERE i.warehouse_id = x.warehouse_id AND i.product_id = x.product_id
            """, connection, tx)
        {
            Parameters = { new() { Value = warehouses }, new() { Value = productIds }, new() { Value = quantities } },
        })
        {
            await update.ExecuteNonQueryAsync(ct);
        }

        // 3. The order in the customer's currency, its lines, the payment and one shipment. The totals are
        //    computed in SQL from the current prices and today's exchange rate.
        await using (var insert = new NpgsqlCommand("""
            WITH customer AS (
                SELECT c.id, co.currency_code,
                       (SELECT a.id FROM store.addresses a WHERE a.customer_id = c.id AND a.kind = 'shipping'
                        ORDER BY a.is_default DESC, a.id LIMIT 1) AS address_id,
                       (SELECT f.units_per_usd FROM store.fx_rates f WHERE f.currency_code = co.currency_code
                        ORDER BY f.rate_date DESC LIMIT 1) AS fx
                FROM store.customers c JOIN store.countries co ON co.code = c.country_code
                WHERE c.id = $1
            ),
            lines AS (
                SELECT x.line_no, x.product_id, x.warehouse_id, x.qty, round(p.price_usd * cu.fx, 2) AS unit_price
                FROM unnest($2::bigint[], $3::bigint[], $4::int[]) WITH ORDINALITY AS x(product_id, warehouse_id, qty, line_no)
                JOIN store.products p ON p.id = x.product_id
                CROSS JOIN customer cu
            ),
            new_order AS (
                INSERT INTO store.orders (customer_id, shipping_address_id, status, currency_code,
                                          subtotal, discount, shipping_fee, tax, total, placed_at, updated_at)
                SELECT cu.id, cu.address_id, 'paid', cu.currency_code, s.subtotal, 0, 0, 0, s.subtotal, now(), now()
                FROM customer cu CROSS JOIN (SELECT sum(unit_price * qty) AS subtotal FROM lines) s
                RETURNING id, total
            ),
            new_lines AS (
                INSERT INTO store.order_items (order_id, line_no, product_id, warehouse_id, quantity, unit_price, discount)
                SELECT o.id, l.line_no, l.product_id, l.warehouse_id, l.qty, l.unit_price, 0
                FROM new_order o CROSS JOIN lines l
            ),
            payment AS (
                INSERT INTO store.payments (order_id, method, status, amount, paid_at)
                SELECT id, 'card', 'captured', total, now() FROM new_order
            )
            INSERT INTO store.shipments (order_id, warehouse_id, carrier, tracking_number, status)
            SELECT o.id, $5, 'Load Test Express', 'LT' || o.id, 'preparing' FROM new_order o
            """, connection, tx)
        {
            Parameters =
            {
                new() { Value = customerId },
                new() { Value = productIds },
                new() { Value = warehouses },
                new() { Value = quantities },
                new() { Value = warehouses[0] },
            },
        })
        {
            await insert.ExecuteNonQueryAsync(ct);
        }

        await tx.CommitAsync(ct);
        return true;
    }
}
