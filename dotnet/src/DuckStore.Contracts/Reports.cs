namespace DuckStore.Contracts;

// Shapes returned by the analytics service (DuckDB) and read by the web app.

public sealed record Report<T>(string Sql, double ElapsedMs, IReadOnlyList<T> Rows);

public sealed record TableReport(string Sql, double ElapsedMs, IReadOnlyList<string> Columns, IReadOnlyList<object?[]> Rows);

public sealed record KpiPeriod(string Period, decimal RevenueUsd, long Orders, decimal AvgOrderUsd, long NewCustomers, long ActiveCustomers);

public sealed record MonthlyRevenue(DateTime Month, decimal RevenueUsd, long Orders);

public sealed record TopProduct(long ProductId, string ProductName, string Brand, string Department, long Units, decimal RevenueUsd);

public sealed record ReturnRate(string Category, string Department, long UnitsSold, long UnitsReturned, double ReturnRatePct, string? TopReason);

public sealed record CountrySales(string Region, string Country, long Orders, long Customers, decimal RevenueUsd);

public sealed record BuildStep(int Number, string Label, long Rows, double Seconds);

/// <summary>What the analytics service has loaded. <see cref="MaxOrderId"/> is the ETL watermark.</summary>
public sealed record WarehouseInfo(
    DateTime BuiltAt, long MaxOrderId, DateTime DataUntil, double BuildSeconds,
    string FilePath, long SizeBytes, string DuckDbVersion, string Threads);
