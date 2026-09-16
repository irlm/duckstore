using DuckStore.Analytics.Warehouse;

namespace DuckStore.Analytics.Api;

// Report endpoints read the DuckDB warehouse. Each response includes the SQL
// and how long DuckDB took.
public static class ReportEndpoints
{
    public static void MapReportEndpoints(this IEndpointRouteBuilder app)
    {
        app.MapGet("/api/warehouse", (ReportService r, CancellationToken ct) => r.InfoAsync(ct))
            .WithTags("Warehouse")
            .WithSummary("When the warehouse was built, the ETL watermark, file size and DuckDB version");

        var reports = app.MapGroup("/api/reports").WithTags("Reports");
        reports.MapGet("/kpis", (ReportService r, CancellationToken ct) => r.KpisAsync(ct))
            .WithSummary("Last 30 days vs the 30 days before");
        reports.MapGet("/monthly-revenue", (ReportService r, CancellationToken ct) => r.MonthlyRevenueAsync(ct));
        reports.MapGet("/top-products", (ReportService r, int days = 90, CancellationToken ct = default) => r.TopProductsAsync(days, ct));
        reports.MapGet("/return-rates", (ReportService r, CancellationToken ct) => r.ReturnRatesAsync(ct));
        reports.MapGet("/sales-by-country", (ReportService r, CancellationToken ct) => r.SalesByCountryAsync(ct));
        reports.MapGet("/revenue-by-department-year", (ReportService r, CancellationToken ct) => r.RevenueByDepartmentAndYearAsync(ct))
            .WithSummary("Dynamic PIVOT: one column per year");
        reports.MapGet("/last-build-steps", (ReportService r, CancellationToken ct) => r.LastBuildStepsAsync(ct));
    }
}
