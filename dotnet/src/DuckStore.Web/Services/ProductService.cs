using System.ComponentModel.DataAnnotations;
using System.Text.Json;
using DuckStore.Web.Data;
using Microsoft.EntityFrameworkCore;
using Npgsql;

namespace DuckStore.Web.Services;

// CRUD for products on Postgres (OLTP): small reads and writes by key, in
// transactions, protected by the database constraints.
// The Blazor pages and the /api/products endpoints both use this class.
//
// Each method creates its own short-lived DbContext from the factory. In Blazor
// Server a "scoped" service lives as long as the browser tab, and a DbContext
// must not be shared by operations that overlap.
public sealed class ProductService(IDbContextFactory<StoreDbContext> contexts)
{
    public async Task<PagedResult<ProductListItem>> ListAsync(ProductQuery q, CancellationToken ct = default)
    {
        await using var db = await contexts.CreateDbContextAsync(ct);
        var query = db.Products.AsNoTracking();
        if (!string.IsNullOrWhiteSpace(q.Search))
        {
            // ILIKE '%text%' is served by the trigram index on products.name.
            var pattern = $"%{q.Search.Trim()}%";
            query = query.Where(p => EF.Functions.ILike(p.Name, pattern) || EF.Functions.ILike(p.Sku, pattern));
        }
        if (q.OnlyActive)
        {
            query = query.Where(p => p.IsActive);
        }

        query = (q.SortBy?.ToLowerInvariant(), q.Descending) switch
        {
            ("price", false) => query.OrderBy(p => p.PriceUsd).ThenBy(p => p.Id),
            ("price", true) => query.OrderByDescending(p => p.PriceUsd).ThenBy(p => p.Id),
            ("name", false) => query.OrderBy(p => p.Name).ThenBy(p => p.Id),
            ("name", true) => query.OrderByDescending(p => p.Name).ThenBy(p => p.Id),
            ("created", false) => query.OrderBy(p => p.CreatedAt).ThenBy(p => p.Id),
            (_, true) => query.OrderByDescending(p => p.Id),
            _ => query.OrderBy(p => p.Id),
        };

        var pageSize = Math.Clamp(q.PageSize, 1, 200);
        var page = Math.Max(q.Page, 1);
        var total = await query.CountAsync(ct);
        var items = await query
            .Skip((page - 1) * pageSize)
            .Take(pageSize)
            .Select(p => new ProductListItem(p.Id, p.Sku, p.Name, p.Brand!.Name, p.Category!.Name, p.PriceUsd, p.IsActive, p.CreatedAt))
            .ToListAsync(ct);
        return new PagedResult<ProductListItem>(items, total);
    }

    public async Task<ProductDetail> GetAsync(long id, CancellationToken ct = default)
    {
        await using var db = await contexts.CreateDbContextAsync(ct);
        var p = await db.Products.AsNoTracking()
            .Include(x => x.Brand)
            .Include(x => x.Category)
            .Include(x => x.Prices.OrderByDescending(pp => pp.ValidFrom))
            .SingleOrDefaultAsync(x => x.Id == id, ct)
            ?? throw new NotFoundException($"Product {id} not found.");

        var orderLines = await db.Database
            .SqlQuery<long>($"SELECT count(*) AS \"Value\" FROM store.order_items WHERE product_id = {id}")
            .SingleAsync(ct);

        return new ProductDetail(
            p.Id, p.Sku, p.Name, p.Description, p.CategoryId, p.Category!.Name, p.BrandId, p.Brand!.Name,
            p.PriceUsd, p.CostUsd, p.WeightKg, p.Attributes, p.IsActive, p.CreatedAt, orderLines,
            p.Prices.Select(pp => new PriceVersion(pp.ValidFrom, pp.ValidTo, pp.PriceUsd)).ToList());
    }

    public async Task<ProductDetail> CreateAsync(ProductInput input, CancellationToken ct = default)
    {
        Validate(input);
        await using var db = await contexts.CreateDbContextAsync(ct);
        var now = DateTime.UtcNow;
        var product = new Product { CreatedAt = now };
        Apply(product, input);
        // The first price version is written in the same SaveChanges (one transaction).
        product.Prices.Add(new ProductPrice { ValidFrom = now, PriceUsd = input.PriceUsd });
        db.Products.Add(product);
        await SaveAsync(db, ct);
        return await GetAsync(product.Id, ct);
    }

    public async Task<ProductDetail> UpdateAsync(long id, ProductInput input, CancellationToken ct = default)
    {
        await using var db = await contexts.CreateDbContextAsync(ct);
        Validate(input);
        var product = await db.Products.SingleOrDefaultAsync(p => p.Id == id, ct)
            ?? throw new NotFoundException($"Product {id} not found.");

        if (product.PriceUsd != input.PriceUsd)
        {
            // New price = new version: close the open row and add a new one.
            // The warehouse turns these rows into a Type 2 slowly changing dimension.
            var now = DateTime.UtcNow;
            var current = await db.ProductPrices.SingleOrDefaultAsync(pp => pp.ProductId == id && pp.ValidTo == null, ct);
            if (current is not null)
            {
                current.ValidTo = now;
            }
            db.ProductPrices.Add(new ProductPrice { ProductId = id, ValidFrom = now, PriceUsd = input.PriceUsd });
        }

        Apply(product, input);
        await SaveAsync(db, ct); // one transaction for the product and its price versions
        return await GetAsync(id, ct);
    }

    public async Task DeleteAsync(long id, CancellationToken ct = default)
    {
        await using var db = await contexts.CreateDbContextAsync(ct);
        await using var tx = await db.Database.BeginTransactionAsync(ct);

        var product = await db.Products.SingleOrDefaultAsync(p => p.Id == id, ct)
            ?? throw new NotFoundException($"Product {id} not found.");

        // Sales history must survive: a product that was sold or reviewed cannot be deleted.
        var used = await db.Database.SqlQuery<long>($"""
            SELECT (SELECT count(*) FROM store.order_items WHERE product_id = {id})
                 + (SELECT count(*) FROM store.reviews WHERE product_id = {id}) AS "Value"
            """).SingleAsync(ct);
        if (used > 0)
        {
            throw new ConflictException($"Product {id} has {used} order lines or reviews. Deactivate it instead of deleting it.");
        }

        // Child rows first: the foreign keys have no ON DELETE CASCADE.
        await db.Database.ExecuteSqlAsync($"DELETE FROM store.cart_items WHERE product_id = {id}", ct);
        await db.Database.ExecuteSqlAsync($"DELETE FROM store.inventory WHERE product_id = {id}", ct);
        await db.Database.ExecuteSqlAsync($"DELETE FROM store.product_suppliers WHERE product_id = {id}", ct);
        await db.Database.ExecuteSqlAsync($"DELETE FROM store.promotion_products WHERE product_id = {id}", ct);
        await db.ProductPrices.Where(pp => pp.ProductId == id).ExecuteDeleteAsync(ct);
        db.Products.Remove(product);
        await SaveAsync(db, ct);
        await tx.CommitAsync(ct);
    }

    public async Task<List<LookupItem>> BrandsAsync(CancellationToken ct = default)
    {
        await using var db = await contexts.CreateDbContextAsync(ct);
        return await db.Brands.AsNoTracking().OrderBy(b => b.Name).Select(b => new LookupItem(b.Id, b.Name)).ToListAsync(ct);
    }

    // Leaf categories with their full path, from a recursive CTE.
    public async Task<List<LookupItem>> CategoriesAsync(CancellationToken ct = default)
    {
        await using var db = await contexts.CreateDbContextAsync(ct);
        return await db.Database.SqlQuery<LookupItem>($"""
            WITH RECURSIVE tree AS (
                SELECT id, name, name::text AS path FROM store.categories WHERE parent_id IS NULL
                UNION ALL
                SELECT c.id, c.name, t.path || ' > ' || c.name
                FROM store.categories c JOIN tree t ON c.parent_id = t.id
            )
            SELECT t.id AS "Id", t.path AS "Name"
            FROM tree t
            WHERE NOT EXISTS (SELECT 1 FROM store.categories k WHERE k.parent_id = t.id)
            ORDER BY t.path
            """).ToListAsync(ct);
    }

    private static void Apply(Product p, ProductInput input)
    {
        p.Sku = input.Sku.Trim().ToUpperInvariant();
        p.Name = input.Name.Trim();
        p.Description = input.Description?.Trim() ?? "";
        p.CategoryId = input.CategoryId;
        p.BrandId = input.BrandId;
        p.PriceUsd = input.PriceUsd;
        p.CostUsd = input.CostUsd;
        p.WeightKg = input.WeightKg;
        p.Attributes = string.IsNullOrWhiteSpace(input.AttributesJson) ? "{}" : input.AttributesJson.Trim();
        p.IsActive = input.IsActive;
    }

    private static void Validate(ProductInput input)
    {
        var results = new List<ValidationResult>();
        Validator.TryValidateObject(input, new ValidationContext(input), results, validateAllProperties: true);
        if (!string.IsNullOrWhiteSpace(input.AttributesJson))
        {
            try
            {
                using var doc = JsonDocument.Parse(input.AttributesJson);
                if (doc.RootElement.ValueKind != JsonValueKind.Object)
                {
                    results.Add(new ValidationResult("Attributes must be a JSON object, like {\"color\": \"red\"}.", [nameof(input.AttributesJson)]));
                }
            }
            catch (JsonException)
            {
                results.Add(new ValidationResult("Attributes is not valid JSON.", [nameof(input.AttributesJson)]));
            }
        }
        if (input.CostUsd > input.PriceUsd)
        {
            results.Add(new ValidationResult("Cost cannot be higher than the price.", [nameof(input.CostUsd)]));
        }
        if (results.Count > 0)
        {
            throw new InputException(results);
        }
    }

    // Turn database constraint errors into messages people can act on.
    private static async Task SaveAsync(StoreDbContext db, CancellationToken ct)
    {
        try
        {
            await db.SaveChangesAsync(ct);
        }
        catch (DbUpdateException e) when (e.InnerException is PostgresException pg)
        {
            db.ChangeTracker.Clear();
            throw pg.SqlState switch
            {
                PostgresErrorCodes.UniqueViolation => new ConflictException($"A product with this SKU already exists ({pg.ConstraintName})."),
                PostgresErrorCodes.ForeignKeyViolation => new InputException([new ValidationResult($"Unknown brand or category ({pg.ConstraintName}).")]),
                PostgresErrorCodes.CheckViolation => new InputException([new ValidationResult($"A value breaks a rule of the table ({pg.ConstraintName}).")]),
                _ => e,
            };
        }
    }
}

public sealed record ProductQuery(string? Search = null, int Page = 1, int PageSize = 20, string? SortBy = null, bool Descending = false, bool OnlyActive = false);

public sealed record PagedResult<T>(IReadOnlyList<T> Items, int Total);

public sealed record ProductListItem(long Id, string Sku, string Name, string Brand, string Category, decimal PriceUsd, bool IsActive, DateTime CreatedAt);

public sealed record PriceVersion(DateTime ValidFrom, DateTime? ValidTo, decimal PriceUsd);

public sealed record ProductDetail(
    long Id, string Sku, string Name, string Description, long CategoryId, string Category, long BrandId, string Brand,
    decimal PriceUsd, decimal CostUsd, decimal WeightKg, string Attributes, bool IsActive, DateTime CreatedAt,
    long OrderLines, IReadOnlyList<PriceVersion> PriceHistory);

public sealed record LookupItem(long Id, string Name);

public sealed class ProductInput
{
    [Required, StringLength(40, MinimumLength = 3)]
    public string Sku { get; set; } = "";

    [Required, StringLength(200, MinimumLength = 2)]
    public string Name { get; set; } = "";

    [StringLength(2000)]
    public string? Description { get; set; }

    [Range(1, long.MaxValue, ErrorMessage = "Choose a category.")]
    public long CategoryId { get; set; }

    [Range(1, long.MaxValue, ErrorMessage = "Choose a brand.")]
    public long BrandId { get; set; }

    [Range(typeof(decimal), "0.01", "100000")]
    public decimal PriceUsd { get; set; }

    [Range(typeof(decimal), "0.01", "100000")]
    public decimal CostUsd { get; set; }

    [Range(typeof(decimal), "0", "10000")]
    public decimal WeightKg { get; set; }

    public string? AttributesJson { get; set; }

    public bool IsActive { get; set; } = true;

    public static ProductInput From(ProductDetail d) => new()
    {
        Sku = d.Sku, Name = d.Name, Description = d.Description, CategoryId = d.CategoryId, BrandId = d.BrandId,
        PriceUsd = d.PriceUsd, CostUsd = d.CostUsd, WeightKg = d.WeightKg, AttributesJson = d.Attributes, IsActive = d.IsActive,
    };
}

public sealed class NotFoundException(string message) : Exception(message);

public sealed class ConflictException(string message) : Exception(message);

public sealed class InputException(IReadOnlyList<ValidationResult> errors)
    : Exception(string.Join(" ", errors.Select(e => e.ErrorMessage)))
{
    public IReadOnlyList<ValidationResult> Errors { get; } = errors;
}
