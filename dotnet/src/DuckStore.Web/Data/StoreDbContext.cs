using System.Text;
using Microsoft.EntityFrameworkCore;

namespace DuckStore.Web.Data;

// EF Core model over the EXISTING Postgres schema "store" (created by `make seed`).
// There are no migrations here: the Go seed owns the schema, this app only maps it.
// Only the tables the CRUD screens need are mapped; reports do not use EF at all.
public sealed class StoreDbContext(DbContextOptions<StoreDbContext> options) : DbContext(options)
{
    public DbSet<Product> Products => Set<Product>();
    public DbSet<ProductPrice> ProductPrices => Set<ProductPrice>();
    public DbSet<Brand> Brands => Set<Brand>();
    public DbSet<Category> Categories => Set<Category>();

    protected override void OnModelCreating(ModelBuilder model)
    {
        model.HasDefaultSchema("store");

        model.Entity<Product>(e =>
        {
            e.ToTable("products");
            e.Property(p => p.Id).UseIdentityByDefaultColumn();
            e.Property(p => p.PriceUsd).HasPrecision(12, 2);
            e.Property(p => p.CostUsd).HasPrecision(12, 2);
            e.Property(p => p.WeightKg).HasPrecision(8, 3);
            e.Property(p => p.Attributes).HasColumnType("jsonb");
            e.HasIndex(p => p.Sku).IsUnique();
            e.HasOne(p => p.Brand).WithMany().HasForeignKey(p => p.BrandId);
            e.HasOne(p => p.Category).WithMany().HasForeignKey(p => p.CategoryId);
            e.HasMany(p => p.Prices).WithOne().HasForeignKey(pp => pp.ProductId);
        });

        // Composite primary key, like the table in Postgres.
        model.Entity<ProductPrice>(e =>
        {
            e.ToTable("product_prices");
            e.HasKey(pp => new { pp.ProductId, pp.ValidFrom });
            e.Property(pp => pp.PriceUsd).HasPrecision(12, 2);
        });

        model.Entity<Brand>().ToTable("brands");
        model.Entity<Category>().ToTable("categories");

        // C# PascalCase properties -> Postgres snake_case columns (PriceUsd -> price_usd).
        foreach (var entity in model.Model.GetEntityTypes())
        {
            foreach (var property in entity.GetProperties())
            {
                property.SetColumnName(ToSnakeCase(property.Name));
            }
        }
    }

    private static string ToSnakeCase(string name)
    {
        var sb = new StringBuilder();
        for (var i = 0; i < name.Length; i++)
        {
            if (char.IsUpper(name[i]) && i > 0)
            {
                sb.Append('_');
            }
            sb.Append(char.ToLowerInvariant(name[i]));
        }
        return sb.ToString();
    }
}

public sealed class Product
{
    public long Id { get; set; }
    public string Sku { get; set; } = "";
    public string Name { get; set; } = "";
    public string Description { get; set; } = "";
    public long CategoryId { get; set; }
    public long BrandId { get; set; }
    public decimal PriceUsd { get; set; }
    public decimal CostUsd { get; set; }
    public decimal WeightKg { get; set; }
    public string Attributes { get; set; } = "{}";
    public bool IsActive { get; set; } = true;
    public DateTime CreatedAt { get; set; }

    public Brand? Brand { get; set; }
    public Category? Category { get; set; }
    public List<ProductPrice> Prices { get; set; } = [];
}

// Price history: one row per version. ValidTo == null is the current price.
public sealed class ProductPrice
{
    public long ProductId { get; set; }
    public DateTime ValidFrom { get; set; }
    public DateTime? ValidTo { get; set; }
    public decimal PriceUsd { get; set; }
}

public sealed class Brand
{
    public long Id { get; set; }
    public string Name { get; set; } = "";
}

public sealed class Category
{
    public long Id { get; set; }
    public long? ParentId { get; set; }
    public string Name { get; set; } = "";
    public string Slug { get; set; } = "";
}
