namespace DuckStore.Analytics.Warehouse;

// Where the warehouse file and the Parquet export live. Relative paths in
// appsettings.json are resolved from the project folder (the content root).
public sealed class WarehouseSettings
{
    public WarehouseSettings(IConfiguration config, IHostEnvironment env)
    {
        WarehousePath = Path.GetFullPath(Path.Combine(env.ContentRootPath, config["Warehouse:Path"] ?? "../../../data/warehouse.duckdb"));
        ParquetDir = config["Warehouse:ParquetDir"] is { Length: > 0 } dir
            ? Path.GetFullPath(Path.Combine(env.ContentRootPath, dir))
            : Path.Combine(Path.GetDirectoryName(WarehousePath)!, "parquet");
        Threads = config.GetValue("Warehouse:Threads", 0);
        MemoryLimit = config["Warehouse:MemoryLimit"] is { Length: > 0 } memory ? memory : null;
        ExtensionDirectory = config["Warehouse:ExtensionDirectory"] is { Length: > 0 } ext ? ext : null;
    }

    public string WarehousePath { get; }
    public string ParquetDir { get; }
    public int Threads { get; }

    /// <summary>
    /// DuckDB's memory_limit, like "8GB" (null = DuckDB's default, 80% of the RAM it sees).
    /// Like SQL Server's "max server memory": when the machine also runs Postgres, set it.
    /// </summary>
    public string? MemoryLimit { get; }

    /// <summary>Where DuckDB installs and loads extensions (null = its default, ~/.duckdb/extensions).</summary>
    public string? ExtensionDirectory { get; }
}
