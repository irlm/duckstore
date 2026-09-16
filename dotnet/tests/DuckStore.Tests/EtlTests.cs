using Dapper;
using DuckDB.NET.Data;
using DuckStore.Web.Data;
using DuckStore.Web.Etl;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

namespace DuckStore.Tests;

public sealed class EtlTests(WebApplicationFactory<Program> factory) : IClassFixture<WebApplicationFactory<Program>>, IDisposable
{
    // The test ETL writes into a temporary folder, never over the real warehouse.
    private readonly string _dir = Directory.CreateTempSubdirectory("duckstore-etl-test-").FullName;

    private WarehouseTarget Target => new(Path.Combine(_dir, "warehouse.duckdb"), Path.Combine(_dir, "parquet"));

    [Fact]
    public async Task A_second_etl_is_refused_while_one_holds_the_lock()
    {
        var builder = factory.Services.GetRequiredService<WarehouseBuilder>();
        await using var held = new FileStream(Path.Combine(_dir, "etl.lock"), FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None);

        await Assert.ThrowsAsync<EtlAlreadyRunningException>(() => builder.BuildAsync(Target));
    }

    [DatabasesFact]
    public async Task Etl_builds_a_warehouse_that_matches_postgres()
    {
        var builder = factory.Services.GetRequiredService<WarehouseBuilder>();
        var progress = new List<EtlStep>();

        var result = await builder.BuildAsync(Target, new SynchronousProgress<EtlStep>(progress.Add));

        Assert.True(File.Exists(Target.WarehousePath));
        Assert.False(File.Exists(Target.WarehousePath + ".building"));
        Assert.True(Directory.EnumerateFiles(Target.ParquetDir, "*.parquet", SearchOption.AllDirectories).Any());
        Assert.Equal(result.Steps.Count, progress.Count);

        // Order lines of non-cancelled orders up to the watermark, counted by Postgres...
        await using var db = await factory.Services.GetRequiredService<IDbContextFactory<StoreDbContext>>().CreateDbContextAsync();
        var expected = await db.Database.SqlQuery<long>($"""
            SELECT count(*) AS "Value"
            FROM store.order_items oi JOIN store.orders o ON o.id = oi.order_id
            WHERE o.status <> 'cancelled' AND o.id <= {result.MaxOrderId}
            """).SingleAsync();

        // ...must equal the rows of dw.fact_sales counted by DuckDB (every line found its product version).
        await using var duck = new DuckDBConnection($"Data Source={Target.WarehousePath};ACCESS_MODE=READ_ONLY");
        await duck.OpenAsync();
        Assert.Equal(expected, await duck.ExecuteScalarAsync<long>("SELECT count(*) FROM dw.fact_sales"));
        Assert.Equal(result.MaxOrderId, await duck.ExecuteScalarAsync<long>("SELECT max_order_id FROM dw.etl_info"));
        Assert.Equal(result.Steps.Count, await duck.ExecuteScalarAsync<long>("SELECT count(*) FROM dw.etl_steps"));
    }

    public void Dispose() => Directory.Delete(_dir, recursive: true);

    // Progress<T> posts to the thread pool; this one reports immediately, so the test sees every step.
    private sealed class SynchronousProgress<T>(Action<T> report) : IProgress<T>
    {
        public void Report(T value) => report(value);
    }
}
