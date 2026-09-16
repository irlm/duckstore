using System.Net.Http.Json;
using DuckStore.Contracts;
using Microsoft.AspNetCore.Mvc.Testing;

namespace DuckStore.Analytics.Tests;

public sealed class AnalyticsTests(WebApplicationFactory<Program> factory) : IClassFixture<WebApplicationFactory<Program>>
{
    private readonly HttpClient _http = factory.CreateClient();

    [DatabasesFact(needsWarehouse: true)]
    public async Task Analytic_returns_rows_and_server_timing()
    {
        using var response = await _http.PostAsJsonAsync("/api/analytics/unusual-days", new AnalyticRequest(MaxOrderId: 0));
        response.EnsureSuccessStatusCode();
        Assert.Contains("execute;dur=", string.Join(",", response.Headers.GetValues("Server-Timing")));

        var result = await response.Content.ReadFromJsonAsync<AnalyticResult>();
        Assert.Equal("duckdb", result!.Engine);
        Assert.Equal(result.RowCount, result.Rows.Count);
        Assert.Contains("order_date", result.Columns);
    }

    [DatabasesFact(needsWarehouse: true)]
    public async Task Plan_is_estimated_or_actual()
    {
        var request = new AnalyticRequest(MaxOrderId: 0, CustomerId: 1);

        var estimated = await PlanAsync("customer-orders", request, analyze: false);
        Assert.False(estimated.Analyze);
        Assert.Contains("SEQ_SCAN", estimated.Text);
        Assert.DoesNotContain("Total Time", estimated.Text);

        var actual = await PlanAsync("customer-orders", request, analyze: true);
        Assert.True(actual.Analyze);
        Assert.Contains("Total Time", actual.Text); // only EXPLAIN ANALYZE runs the query and measures it
    }

    [DatabasesFact(needsWarehouse: true)]
    public async Task Unknown_analytic_is_404()
    {
        using var response = await _http.PostAsJsonAsync("/api/analytics/no-such-question/plan", new AnalyticRequest(0));
        Assert.Equal(System.Net.HttpStatusCode.NotFound, response.StatusCode);
    }

    private async Task<AnalyticPlan> PlanAsync(string id, AnalyticRequest request, bool analyze)
    {
        using var response = await _http.PostAsJsonAsync($"/api/analytics/{id}/plan?analyze={analyze}", request);
        response.EnsureSuccessStatusCode();
        return (await response.Content.ReadFromJsonAsync<AnalyticPlan>())!;
    }
}
