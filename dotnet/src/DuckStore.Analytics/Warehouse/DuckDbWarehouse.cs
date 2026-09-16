using Dapper;
using DuckDB.NET.Data;

namespace DuckStore.Analytics.Warehouse;

// Read-only access to the DuckDB warehouse built by the ETL (WarehouseBuilder).
//
// DuckDB is a library: there is no server to connect to. This class keeps one
// in-memory DuckDB instance with the warehouse file attached READ_ONLY:
//
//     root (Data Source=:memory:)
//       └── ATTACH '.../warehouse.duckdb' AS wh (READ_ONLY)
//
// Every query gets its own connection with root.Duplicate() (about 0.4 ms)
// instead of opening the file again (about 13 ms).
//
// The ETL writes a NEW file and renames it over the old one. When the file
// changes, the next query opens a new root on the new file. Queries still
// running on the old root finish normally: a duplicate keeps the old instance
// alive until it is disposed.
public sealed class DuckDbWarehouse : IDisposable
{
    private readonly string _path;
    private readonly int _threads;
    private readonly string? _memoryLimit;
    private readonly ILogger<DuckDbWarehouse> _log;
    private readonly Lock _gate = new();
    private DuckDBConnection? _root;
    private (DateTime WrittenAt, long Length) _stamp;

    public DuckDbWarehouse(WarehouseSettings settings, ILogger<DuckDbWarehouse> log)
    {
        _path = settings.WarehousePath;
        _threads = settings.Threads;
        _memoryLimit = settings.MemoryLimit;
        _log = log;
    }

    public string FilePath => _path;

    /// <summary>Opens a connection to the latest warehouse. Dispose it when done.</summary>
    public async Task<DuckDBConnection> OpenAsync(CancellationToken ct = default)
    {
        var connection = CurrentRoot().Duplicate();
        await connection.OpenAsync(ct);
        await connection.ExecuteAsync("USE wh"); // so the SQL can say dw.fact_sales instead of wh.dw.fact_sales
        return connection;
    }

    private DuckDBConnection CurrentRoot()
    {
        var file = new FileInfo(_path);
        if (!file.Exists)
        {
            throw new WarehouseNotBuiltException(_path);
        }
        var stamp = (file.LastWriteTimeUtc, file.Length);

        lock (_gate)
        {
            if (_root is not null && stamp == _stamp)
            {
                return _root;
            }

            var root = new DuckDBConnection("Data Source=:memory:");
            root.Open();
            root.Execute($"ATTACH '{_path.Replace("'", "''")}' AS wh (READ_ONLY)");
            // Like the ETL and Postgres: casting a timestamp with time zone to a date cuts the day
            // at midnight UTC, not at midnight of the machine's time zone. GLOBAL: every duplicate
            // connection gets it.
            root.Execute("SET GLOBAL TimeZone = 'UTC'");
            if (_threads > 0)
            {
                // DuckDB uses every core by default. Limit it when this service
                // shares the machine with other busy services.
                root.Execute($"SET threads = {_threads}");
            }
            if (_memoryLimit is not null)
            {
                root.Execute($"SET memory_limit = '{_memoryLimit.Replace("'", "''")}'");
            }

            var old = _root;
            (_root, _stamp) = (root, stamp);
            old?.Dispose();
            _log.LogInformation("Opened warehouse {Path} ({Megabytes:F0} MB, written {WrittenAt:u})", _path, file.Length / 1e6, file.LastWriteTimeUtc);
            return root;
        }
    }

    public void Dispose()
    {
        lock (_gate)
        {
            _root?.Dispose();
            _root = null;
        }
    }
}

public sealed class WarehouseNotBuiltException(string path)
    : Exception($"The warehouse file {path} does not exist yet. Run the ETL (the ETL page, POST /api/etl/runs, or `dotnet DuckStore.Analytics.dll etl`).");
