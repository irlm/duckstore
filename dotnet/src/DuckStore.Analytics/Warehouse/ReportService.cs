using System.Diagnostics;
using Dapper;
using DuckStore.Contracts;

namespace DuckStore.Analytics.Warehouse;

// Reports run on the DuckDB warehouse (OLAP) with Dapper and plain SQL:
// big scans and aggregations, where EF Core would only add a layer.
// The SQL is the same as the Go dashboard; only the host language changed.
//
// Two DuckDB-to-.NET type notes:
//   * sum() of an INTEGER column returns HUGEINT (System.Numerics.BigInteger),
//     so the SQL casts those sums to BIGINT.
//   * DATE comes back as DateOnly; the SQL casts to TIMESTAMP where a DateTime is easier.
public sealed class ReportService(DuckDbWarehouse warehouse, WarehouseSettings settings)
{
    public Task<Report<KpiPeriod>> KpisAsync(CancellationToken ct = default) => QueryAsync<KpiPeriod>("""
        WITH bounds AS (
            SELECT max(order_date) AS last_day FROM dw.fact_orders
        ),
        periods AS (
            SELECT CASE WHEN fo.order_date > b.last_day - 30 THEN 'current' ELSE 'previous' END AS period, fo.*
            FROM dw.fact_orders fo, bounds b
            WHERE fo.order_date > b.last_day - 60
              AND fo.status <> 'cancelled'
        )
        SELECT period                                              AS Period,
               sum(total_usd)                                      AS RevenueUsd,
               count(*)                                            AS Orders,
               round(sum(total_usd) / count(*), 2)::DECIMAL(14,2)  AS AvgOrderUsd,  -- division gives DOUBLE; the record wants decimal
               count(*) FILTER (WHERE customer_order_number = 1)   AS NewCustomers,
               count(DISTINCT customer_id)                         AS ActiveCustomers
        FROM periods
        GROUP BY period
        ORDER BY period
        """, null, ct);

    public Task<Report<MonthlyRevenue>> MonthlyRevenueAsync(CancellationToken ct = default) => QueryAsync<MonthlyRevenue>("""
        SELECT date_trunc('month', order_date)::TIMESTAMP AS Month,
               sum(net_usd)                               AS RevenueUsd,
               count(DISTINCT order_id)                   AS Orders
        FROM dw.fact_sales
        GROUP BY ALL
        ORDER BY Month
        """, null, ct);

    public Task<Report<TopProduct>> TopProductsAsync(int days, CancellationToken ct = default) => QueryAsync<TopProduct>("""
        SELECT dp.product_id            AS ProductId,
               dp.product_name          AS ProductName,
               dp.brand                 AS Brand,
               dp.category_l1           AS Department,
               sum(fs.quantity)::BIGINT AS Units,
               sum(fs.net_usd)          AS RevenueUsd
        FROM dw.fact_sales fs
        JOIN dw.dim_product dp USING (product_key)
        WHERE fs.order_date > (SELECT max(order_date) FROM dw.fact_sales) - CAST($days AS INTEGER)
        GROUP BY ALL
        ORDER BY RevenueUsd DESC
        LIMIT 10
        """, new { days = Math.Clamp(days, 1, 3650) }, ct);

    public Task<Report<ReturnRate>> ReturnRatesAsync(CancellationToken ct = default) => QueryAsync<ReturnRate>("""
        SELECT dp.category_leaf                                                        AS Category,
               dp.category_l1                                                          AS Department,
               sum(fs.quantity)::BIGINT                                                AS UnitsSold,
               coalesce(sum(fr.quantity), 0)::BIGINT                                   AS UnitsReturned,
               round(100.0 * coalesce(sum(fr.quantity), 0) / sum(fs.quantity), 2)::DOUBLE AS ReturnRatePct,
               mode(fr.reason)                                                         AS TopReason
        FROM dw.fact_sales fs
        JOIN dw.dim_product dp USING (product_key)
        LEFT JOIN dw.fact_returns fr USING (order_id, line_no)
        WHERE fs.order_status = 'delivered'
        GROUP BY ALL
        ORDER BY ReturnRatePct DESC
        LIMIT 10
        """, null, ct);

    public Task<Report<CountrySales>> SalesByCountryAsync(CancellationToken ct = default) => QueryAsync<CountrySales>("""
        SELECT dc.region                     AS Region,
               dc.country                    AS Country,
               count(*)                      AS Orders,
               count(DISTINCT fo.customer_id) AS Customers,
               sum(fo.total_usd)             AS RevenueUsd
        FROM dw.fact_orders fo
        JOIN dw.dim_customer dc USING (customer_id)
        WHERE fo.status <> 'cancelled'
        GROUP BY ALL
        ORDER BY RevenueUsd DESC
        """, null, ct);

    // PIVOT creates one column per year, so the columns are only known at run time.
    public async Task<TableReport> RevenueByDepartmentAndYearAsync(CancellationToken ct = default)
    {
        const string sql = """
            PIVOT (
                SELECT dp.category_l1 AS department, year(fs.order_date) AS year, fs.net_usd
                FROM dw.fact_sales fs
                JOIN dw.dim_product dp USING (product_key)
            )
            ON year
            USING round(sum(net_usd) / 1e6, 2)
            GROUP BY department
            ORDER BY department
            """;
        var started = Stopwatch.GetTimestamp();
        await using var connection = await warehouse.OpenAsync(ct);
        await using var reader = await connection.ExecuteReaderAsync(new CommandDefinition(sql, cancellationToken: ct));
        var columns = Enumerable.Range(0, reader.FieldCount).Select(reader.GetName).ToList();
        var rows = new List<object?[]>();
        while (await reader.ReadAsync(ct))
        {
            var values = new object[reader.FieldCount];
            reader.GetValues(values);
            rows.Add(values.Select(v => v is DBNull ? null : v).ToArray<object?>());
        }
        return new TableReport(sql, Stopwatch.GetElapsedTime(started).TotalMilliseconds, columns, rows);
    }

    // What is loaded: the ETL watermark and build time, plus engine facts.
    public async Task<WarehouseInfo> InfoAsync(CancellationToken ct = default)
    {
        await using var connection = await warehouse.OpenAsync(ct);
        var etl = await connection.QuerySingleAsync<EtlInfo>(new CommandDefinition("""
            SELECT built_at AS BuiltAt, max_order_id AS MaxOrderId, data_until AS DataUntil, build_seconds::DOUBLE AS BuildSeconds
            FROM dw.etl_info
            """, cancellationToken: ct));
        var version = await connection.ExecuteScalarAsync<string>("SELECT version()");
        var threads = await connection.ExecuteScalarAsync<string>("SELECT current_setting('threads')::VARCHAR");
        return new WarehouseInfo(etl.BuiltAt, etl.MaxOrderId, etl.DataUntil, etl.BuildSeconds, settings.WarehousePath,
            new FileInfo(settings.WarehousePath).Length, version ?? "", threads ?? "");
    }

    public Task<Report<BuildStep>> LastBuildStepsAsync(CancellationToken ct = default) => QueryAsync<BuildStep>("""
        SELECT step_no AS Number, label AS Label, row_count AS Rows, seconds AS Seconds
        FROM dw.etl_steps
        ORDER BY step_no
        """, null, ct);

    public async Task<IReadOnlyList<EtlRunRecord>> EtlRunsAsync(CancellationToken ct = default)
    {
        await using var connection = await warehouse.OpenAsync(ct);
        var exists = await connection.ExecuteScalarAsync<long>(
            "SELECT count(*) FROM duckdb_tables() WHERE schema_name = 'dw' AND table_name = 'etl_runs'");
        if (exists == 0) return []; // built before load history existed
        return (await connection.QueryAsync<EtlRunRecord>(new CommandDefinition("""
            SELECT finished_at AS FinishedAt, mode AS Mode, from_order_id AS FromOrderId, to_order_id AS ToOrderId,
                   changed_orders AS ChangedOrders, seconds AS Seconds
            FROM dw.etl_runs
            ORDER BY finished_at DESC
            LIMIT 50
            """, cancellationToken: ct))).ToList();
    }

    private async Task<Report<T>> QueryAsync<T>(string sql, object? parameters, CancellationToken ct)
    {
        var started = Stopwatch.GetTimestamp();
        await using var connection = await warehouse.OpenAsync(ct);
        var rows = (await connection.QueryAsync<T>(new CommandDefinition(sql, parameters, cancellationToken: ct))).ToList();
        return new Report<T>(sql, Stopwatch.GetElapsedTime(started).TotalMilliseconds, rows);
    }
}

internal sealed record EtlInfo(DateTime BuiltAt, long MaxOrderId, DateTime DataUntil, double BuildSeconds);
