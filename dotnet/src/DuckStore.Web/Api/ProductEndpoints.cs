using DuckStore.Web.Services;

namespace DuckStore.Web.Api;

// REST endpoints for other clients (mobile app, scripts, tests).
// The Blazor pages call ProductService directly, in the same process.
public static class ProductEndpoints
{
    public static void MapProductEndpoints(this IEndpointRouteBuilder app)
    {
        var products = app.MapGroup("/api/products").WithTags("Products (Postgres)");

        products.MapGet("/", (ProductService service, string? search, int page = 1, int pageSize = 20,
                string? sortBy = null, bool descending = false, bool onlyActive = false, CancellationToken ct = default) =>
            service.ListAsync(new ProductQuery(search, page, pageSize, sortBy, descending, onlyActive), ct))
            .WithSummary("List products with search, sorting and paging");

        products.MapGet("/{id:long}", (long id, ProductService service, CancellationToken ct) => service.GetAsync(id, ct))
            .WithSummary("One product with its price history");

        products.MapPost("/", async (ProductInput input, ProductService service, CancellationToken ct) =>
            {
                var created = await service.CreateAsync(input, ct);
                return Results.Created($"/api/products/{created.Id}", created);
            })
            .WithSummary("Create a product (and its first price version)");

        products.MapPut("/{id:long}", (long id, ProductInput input, ProductService service, CancellationToken ct) =>
                service.UpdateAsync(id, input, ct))
            .WithSummary("Update a product; a new price creates a new price version");

        products.MapDelete("/{id:long}", async (long id, ProductService service, CancellationToken ct) =>
            {
                await service.DeleteAsync(id, ct);
                return Results.NoContent();
            })
            .WithSummary("Delete a product that was never sold or reviewed");

        var lookups = app.MapGroup("/api/lookups").WithTags("Lookups (Postgres)");
        lookups.MapGet("/brands", (ProductService service, CancellationToken ct) => service.BrandsAsync(ct));
        lookups.MapGet("/categories", (ProductService service, CancellationToken ct) => service.CategoriesAsync(ct))
            .WithSummary("Leaf categories with their full path (recursive CTE)");
    }
}
