using System.Net;
using System.Net.Http.Json;
using System.Net.Sockets;
using System.Text.Json;
using DuckStore.Web.Services;
using DuckStore.Web.Warehouse;
using Microsoft.AspNetCore.Mvc.Testing;

namespace DuckStore.Tests;

// Integration tests: the real app in memory (WebApplicationFactory), talking to
// the real Postgres and the real DuckDB warehouse. They are skipped when those
// are not available (run `make up seed etl` in the duckstore folder first).
public sealed class ApiTests(WebApplicationFactory<Program> factory) : IClassFixture<WebApplicationFactory<Program>>
{
    private readonly HttpClient _http = factory.CreateClient();

    [DatabasesFact]
    public async Task Product_can_be_created_repriced_and_deleted()
    {
        var input = NewInput($"TEST-{Guid.NewGuid():N}"[..20], price: 10m);

        var create = await _http.PostAsJsonAsync("/api/products", input);
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var created = await create.Content.ReadFromJsonAsync<ProductDetail>();
        Assert.NotNull(created);
        Assert.Single(created.PriceHistory);

        input.PriceUsd = 12.5m;
        var updated = await (await _http.PutAsJsonAsync($"/api/products/{created.Id}", input)).Content.ReadFromJsonAsync<ProductDetail>();
        Assert.NotNull(updated);
        Assert.Equal(12.5m, updated.PriceUsd);
        Assert.Equal(2, updated.PriceHistory.Count);
        Assert.Null(updated.PriceHistory[0].ValidTo);                                  // new version is open
        Assert.Equal(updated.PriceHistory[0].ValidFrom, updated.PriceHistory[1].ValidTo); // old one closed at the same moment

        Assert.Equal(HttpStatusCode.NoContent, (await _http.DeleteAsync($"/api/products/{created.Id}")).StatusCode);
        Assert.Equal(HttpStatusCode.NotFound, (await _http.GetAsync($"/api/products/{created.Id}")).StatusCode);
    }

    [DatabasesFact]
    public async Task Duplicate_sku_is_a_conflict()
    {
        var input = NewInput($"TEST-{Guid.NewGuid():N}"[..20], price: 10m);
        var first = await (await _http.PostAsJsonAsync("/api/products", input)).Content.ReadFromJsonAsync<ProductDetail>();
        try
        {
            var second = await _http.PostAsJsonAsync("/api/products", input);
            Assert.Equal(HttpStatusCode.Conflict, second.StatusCode);
        }
        finally
        {
            await _http.DeleteAsync($"/api/products/{first!.Id}");
        }
    }

    [DatabasesFact]
    public async Task Invalid_input_returns_field_errors()
    {
        var input = NewInput("X", price: 1m);
        input.CostUsd = 5m;
        input.AttributesJson = "[1, 2]";

        var response = await _http.PostAsJsonAsync("/api/products", input);

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<JsonElement>();
        var errors = problem.GetProperty("errors");
        Assert.True(errors.TryGetProperty("Sku", out _));
        Assert.True(errors.TryGetProperty("CostUsd", out _));
        Assert.True(errors.TryGetProperty("AttributesJson", out _));
    }

    [DatabasesFact]
    public async Task A_sold_product_cannot_be_deleted()
    {
        var top = await _http.GetFromJsonAsync<Report<TopProduct>>("/api/reports/top-products?days=365");
        var sold = top!.Rows.First().ProductId; // the warehouse says it was sold

        var response = await _http.DeleteAsync($"/api/products/{sold}");

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [DatabasesFact]
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

        var status = await _http.GetFromJsonAsync<WarehouseStatus>("/api/reports/status");
        Assert.True(status!.MaxOrderId > 0);
        Assert.True(status.OrdersSinceEtl >= 0);
    }

    private static ProductInput NewInput(string sku, decimal price) => new()
    {
        Sku = sku,
        Name = "Integration Test Product",
        Description = "Created by DuckStore.Tests",
        CategoryId = 62,
        BrandId = 1,
        PriceUsd = price,
        CostUsd = price / 2,
        WeightKg = 1,
        AttributesJson = """{"color": "yellow"}""",
        IsActive = true,
    };
}

// A [Fact] that is skipped when Postgres or the warehouse file is missing.
public sealed class DatabasesFactAttribute : FactAttribute
{
    public DatabasesFactAttribute()
    {
        if (!PostgresIsUp())
        {
            Skip = "Postgres is not reachable on 127.0.0.1:55432. Run `make up && make seed`.";
        }
        else if (!File.Exists(WarehousePath()))
        {
            Skip = "The DuckDB warehouse is not built. Run `make etl`.";
        }
    }

    private static bool PostgresIsUp()
    {
        try
        {
            using var tcp = new TcpClient();
            return tcp.ConnectAsync("127.0.0.1", 55432).Wait(TimeSpan.FromSeconds(1));
        }
        catch (Exception)
        {
            return false;
        }
    }

    // The repository root is the folder that has docker-compose.yml.
    private static string WarehousePath()
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir is not null && !File.Exists(Path.Combine(dir.FullName, "docker-compose.yml")))
        {
            dir = dir.Parent;
        }
        return dir is null ? "" : Path.Combine(dir.FullName, "data", "warehouse.duckdb");
    }
}
