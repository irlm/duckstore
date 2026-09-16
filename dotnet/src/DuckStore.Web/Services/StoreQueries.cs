using DuckStore.Web.Data;
using Microsoft.EntityFrameworkCore;

namespace DuckStore.Web.Services;

// Small live questions for Postgres, the source of truth.
public sealed class StoreQueries(IDbContextFactory<StoreDbContext> contexts)
{
    /// <summary>Orders placed after the warehouse watermark: data DuckDB does not have yet.</summary>
    public async Task<long> OrdersAfterAsync(long maxOrderId, CancellationToken ct = default)
    {
        await using var db = await contexts.CreateDbContextAsync(ct);
        return await db.Database.SqlQuery<long>($"SELECT count(*) AS \"Value\" FROM store.orders WHERE id > {maxOrderId}").SingleAsync(ct);
    }
}
