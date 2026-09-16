using System.Net.Http.Json;
using DuckStore.Contracts;
using Microsoft.AspNetCore.Mvc.Testing;

namespace DuckStore.Analytics.Tests;

public sealed class ReportTests(WebApplicationFactory<Program> factory) : IClassFixture<WebApplicationFactory<Program>>
{
    private readonly HttpClient _http = factory.CreateClient();

    [DatabasesFact(needsWarehouse: true)]
    public async Task Reports_return_rows_sql_and_timing()
    {
        var monthly = await _http.GetFromJsonAsync<Report<MonthlyRevenue>>("/api/reports/monthly-revenue");
        Assert.NotNull(monthly);
        Assert.True(monthly.Rows.Count >= 12);
        Assert.All(monthly.Rows, r => Assert.True(r.RevenueUsd > 0));
        Assert.Contains("dw.fact_sales", monthly.Sql);

        var pivot = await _http.GetFromJsonAsync<TableReport>("/api/reports/revenue-by-department-year");
        Assert.NotNull(pivot);
        Assert.Equal("department", pivot.Columns[0]);
        Assert.True(pivot.Columns.Count >= 3, "PIVOT should create one column per year");

        var info = await _http.GetFromJsonAsync<WarehouseInfo>("/api/warehouse");
        Assert.True(info!.MaxOrderId > 0);
        Assert.StartsWith("v", info.DuckDbVersion);
    }
}
