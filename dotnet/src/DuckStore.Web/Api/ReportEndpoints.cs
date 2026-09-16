using DuckStore.Web.Warehouse;

namespace DuckStore.Web.Api;

// Report endpoints read the DuckDB warehouse. Each response includes the SQL
// and how long DuckDB took, so API clients can learn from it too.
public static class ReportEndpoints
{
    public static void MapReportEndpoints(this IEndpointRouteBuilder app)
    {
        var reports = app.MapGroup("/api/reports").WithTags("Reports (DuckDB)");

        reports.MapGet("/status", (ReportService r, CancellationToken ct) => r.StatusAsync(ct))
            .WithSummary("When the warehouse was built, and how many orders Postgres has that it does not");
        reports.MapGet("/kpis", (ReportService r, CancellationToken ct) => r.KpisAsync(ct))
            .WithSummary("Last 30 days vs the 30 days before");
        reports.MapGet("/monthly-revenue", (ReportService r, CancellationToken ct) => r.MonthlyRevenueAsync(ct));
        reports.MapGet("/top-products", (ReportService r, int days = 90, CancellationToken ct = default) => r.TopProductsAsync(days, ct));
        reports.MapGet("/return-rates", (ReportService r, CancellationToken ct) => r.ReturnRatesAsync(ct));
        reports.MapGet("/sales-by-country", (ReportService r, CancellationToken ct) => r.SalesByCountryAsync(ct));
        reports.MapGet("/revenue-by-department-year", (ReportService r, CancellationToken ct) => r.RevenueByDepartmentAndYearAsync(ct))
            .WithSummary("Dynamic PIVOT: one column per year");
    }
}
