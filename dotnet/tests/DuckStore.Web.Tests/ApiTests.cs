using System.Net;
using System.Net.Http.Json;
using System.Net.Sockets;
using System.Text.Json;
using DuckStore.Web.Data;
using DuckStore.Web.Services;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

namespace DuckStore.Web.Tests;

// Integration tests: the real web app in memory (WebApplicationFactory), talking
// to the real Postgres. Skipped when Postgres is not available (`make up seed`).
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
        await using var db = await factory.Services.GetRequiredService<IDbContextFactory<StoreDbContext>>().CreateDbContextAsync();
        var sold = await db.Database.SqlQuery<long>($"SELECT product_id AS \"Value\" FROM store.order_items LIMIT 1").SingleAsync();

        var response = await _http.DeleteAsync($"/api/products/{sold}");

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
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

// A [Fact] that is skipped when Postgres is not reachable.
public sealed class DatabasesFactAttribute : FactAttribute
{
    public DatabasesFactAttribute()
    {
        if (!PostgresIsUp())
        {
            Skip = "Postgres is not reachable on 127.0.0.1:55432. Run `make up && make seed`.";
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
}
