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

    [Fact]
    public void Store_sql_for_duckdb_changes_only_schema_and_parameter_prefix()
    {
        const string postgres = """
            -- the normalized store tables
            SELECT o.id, c.name FROM store.orders o JOIN store.customers c ON c.id = o.customer_id
            WHERE o.id <= @max_order_id AND c.id = @customer_id AND o.status <> 'cancelled'
            """;
        var duckdb = Analytics.AnalyticsRunner.StoreSqlForDuckDb(postgres);
        Assert.Contains("FROM raw.orders o JOIN raw.customers c", duckdb);
        Assert.Contains("o.id <= $max_order_id AND c.id = $customer_id", duckdb);
        Assert.Contains("-- the normalized store tables", duckdb);
        Assert.Contains("o.status <> 'cancelled'", duckdb);
    }

    [DatabasesFact(needsWarehouse: true)]
    public async Task Store_and_star_models_give_the_same_result()
    {
        var info = await _http.GetFromJsonAsync<WarehouseInfo>("/api/warehouse");
        foreach (var id in new[] { "brand-returns", "unusual-days", "clv-buckets" })
        {
            var store = await RunAsync(id, new AnalyticRequest(info!.MaxOrderId, 1, DataModel.Store));
            var star = await RunAsync(id, new AnalyticRequest(info.MaxOrderId, 1, DataModel.Star));
            Assert.Contains("raw.", store.Sql);
            Assert.Contains("dw.", star.Sql);
            Assert.Equal(star.RowCount, store.RowCount);
            for (var r = 0; r < star.Rows.Count; r++)
            {
                for (var c = 0; c < star.Columns.Count; c++)
                {
                    Assert.True(Cells.Same(Cells.FromJson((System.Text.Json.JsonElement)star.Rows[r][c]!), Cells.FromJson((System.Text.Json.JsonElement)store.Rows[r][c]!)),
                        $"{id} row {r + 1} column {star.Columns[c]}: star {star.Rows[r][c]}, store {store.Rows[r][c]}");
                }
            }
        }
    }

    private async Task<AnalyticResult> RunAsync(string id, AnalyticRequest request)
    {
        using var response = await _http.PostAsJsonAsync($"/api/analytics/{id}", request);
        response.EnsureSuccessStatusCode();
        return (await response.Content.ReadFromJsonAsync<AnalyticResult>())!;
    }

    private async Task<AnalyticPlan> PlanAsync(string id, AnalyticRequest request, bool analyze)
    {
        using var response = await _http.PostAsJsonAsync($"/api/analytics/{id}/plan?analyze={analyze}", request);
        response.EnsureSuccessStatusCode();
        return (await response.Content.ReadFromJsonAsync<AnalyticPlan>())!;
    }
}
