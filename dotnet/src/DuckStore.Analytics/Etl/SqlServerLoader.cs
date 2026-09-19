using System.Data;
using System.Data.Common;
using System.Globalization;
using System.Reflection;
using System.Text;
using DuckDB.NET.Data;
using DuckStore.Analytics.Warehouse;
using Microsoft.Data.SqlClient;

namespace DuckStore.Analytics.Etl;

// `mssql-load`: copy the warehouse into SQL Server, so the third engine answers the same
// questions on the same rows.
//
//   store schema  the store tables as the application writes them (rowstore, the same primary
//                 keys and indexes Postgres has, so neither engine gets a head start)
//   dw schema     the star schema, with a clustered columnstore index on the facts — the
//                 feature SQL Server teams reach for, and DuckDB's real competitor
//
// The tables are created from the warehouse's own schema (DuckDB information_schema), so a new
// column in the ETL does not need a second definition here. Data moves with SqlBulkCopy,
// streamed straight out of a DuckDB reader: no CSV files, no shared folder, and it works over
// the network from another machine.
public sealed class SqlServerLoader(DuckDbWarehouse warehouse, IConfiguration config, ILogger<SqlServerLoader> log)
{
    // Only what the 15 questions read, in dependency-free order: a full copy would double the load time.
    public static readonly string[] StoreTables =
        ["brands", "categories", "products", "customers", "employees", "fx_rates", "fx_rates_daily", "orders", "order_items", "shipments", "returns"];

    public static readonly string[] StarTables =
        ["dim_category", "dim_customer", "dim_employee", "dim_product", "fact_orders", "fact_sales", "fact_returns"];

    private const int DefaultBatchSize = 50_000;

    public async Task<int> RunAsync(IReadOnlyList<string> args, CancellationToken ct = default)
    {
        var models = new List<string> { "store", "star" };
        IReadOnlyList<string>? only = null;
        var batchSize = DefaultBatchSize;
        var columnstore = true;
        var verifyOnly = false;
        string? connectionString = null;

        for (var i = 0; i < args.Count; i++)
        {
            switch (args[i])
            {
                case "--connection": connectionString = args[++i]; break;
                case "--models": models = args[++i].Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries).ToList(); break;
                case "--tables": only = args[++i].Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries); break;
                case "--batch-size": batchSize = int.Parse(args[++i], CultureInfo.InvariantCulture); break;
                case "--no-columnstore": columnstore = false; break;
                case "--verify": verifyOnly = true; break;
                case "-h" or "--help": Usage(); return 0;
                default: log.LogError("Unknown argument '{Arg}'.", args[i]); Usage(); return 1;
            }
        }

        connectionString ??= config.GetConnectionString("SqlServer");
        if (string.IsNullOrWhiteSpace(connectionString))
        {
            log.LogError("No SQL Server connection: pass --connection or set ConnectionStrings:SqlServer.");
            return 1;
        }

        var builder = new SqlConnectionStringBuilder(connectionString);
        var database = builder.InitialCatalog is { Length: > 0 } name ? name : "duckstore";
        await EnsureDatabaseAsync(builder, database, ct);

        await using var duck = await warehouse.OpenAsync(ct);
        await using var sql = new SqlConnection(builder.ConnectionString);
        await sql.OpenAsync(ct);

        var tables = new List<(string Model, string DuckSchema, string SqlSchema, string Table)>();
        if (models.Contains("store")) tables.AddRange(StoreTables.Select(t => ("store", "raw", "store", t)));
        if (models.Contains("star")) tables.AddRange(StarTables.Select(t => ("star", "dw", "dw", t)));
        if (only is not null) tables = tables.Where(t => only.Contains(t.Table)).ToList();
        if (tables.Count == 0)
        {
            log.LogError("Nothing to load: check --models and --tables.");
            return 1;
        }

        if (verifyOnly) return await VerifyAsync(duck, sql, tables, ct) ? 0 : 1;

        foreach (var schema in tables.Select(t => t.SqlSchema).Distinct())
        {
            await Execute(sql, $"IF SCHEMA_ID('{schema}') IS NULL EXEC('CREATE SCHEMA [{schema}]')", ct);
        }

        var total = System.Diagnostics.Stopwatch.StartNew();
        foreach (var (_, duckSchema, sqlSchema, table) in tables)
        {
            await CopyTableAsync(duck, sql, duckSchema, sqlSchema, table, batchSize, ct);
        }

        // Keys and indexes come after the data: building them once is far cheaper than
        // maintaining them through 26 million inserts.
        foreach (var model in models)
        {
            var file = model == "store" ? "Mssql.01_store_indexes.sql" : "Mssql.02_star_indexes.sql";
            if (model == "star" && !columnstore) file = "Mssql.03_star_rowstore.sql";
            await RunScriptAsync(sql, file, ct);
        }

        log.LogInformation("Loaded {Count} tables in {Seconds:N1} s.", tables.Count, total.Elapsed.TotalSeconds);
        return await VerifyAsync(duck, sql, tables, ct) ? 0 : 1;
    }

    // ── schema ────────────────────────────────────────────────────────────────

    /// <summary>A column of the warehouse, and what it becomes in SQL Server.</summary>
    internal sealed record Column(string Name, string DuckType, string SqlType, string Select);

    /// <summary>
    /// DuckDB type to SQL Server type. Nested types (lists, structs, JSON) travel as JSON text,
    /// because SQL Server has no column type for them; timestamps arrive in UTC, as the warehouse
    /// stores them.
    /// </summary>
    internal static Column MapColumn(string name, string duckType, int? maxLength)
    {
        var quoted = $"\"{name.Replace("\"", "\"\"")}\"";
        var upper = duckType.ToUpperInvariant();

        string sqlType, select = quoted;
        if (upper.StartsWith("DECIMAL", StringComparison.Ordinal) || upper.StartsWith("NUMERIC", StringComparison.Ordinal))
        {
            sqlType = upper.Replace("NUMERIC", "DECIMAL", StringComparison.Ordinal);
        }
        else if (upper is "VARCHAR" or "TEXT" or "STRING" or "UUID" or "JSON" || upper.EndsWith("]", StringComparison.Ordinal)
                 || upper.StartsWith("STRUCT", StringComparison.Ordinal) || upper.StartsWith("MAP", StringComparison.Ordinal)
                 || upper.StartsWith("LIST", StringComparison.Ordinal))
        {
            // A list or struct becomes its JSON text; a plain string keeps a length that fits the
            // data, because varchar(max) is slower to scan and cannot be an index key.
            var nested = upper.EndsWith("]", StringComparison.Ordinal) || upper.StartsWith("STRUCT", StringComparison.Ordinal)
                         || upper.StartsWith("MAP", StringComparison.Ordinal) || upper.StartsWith("LIST", StringComparison.Ordinal);
            if (nested) select = $"to_json({quoted})::VARCHAR";
            else if (upper is "UUID" or "JSON") select = $"{quoted}::VARCHAR";

            sqlType = maxLength is null or > 4000 ? "varchar(max)" : $"varchar({Math.Max(1, maxLength.Value)})";
        }
        else
        {
            sqlType = upper switch
            {
                "BIGINT" or "INT8" or "LONG" => "bigint",
                "INTEGER" or "INT4" or "INT" or "SIGNED" => "int",
                "SMALLINT" or "INT2" or "SHORT" => "smallint",
                "TINYINT" or "INT1" => "tinyint",
                "UTINYINT" or "USMALLINT" => "int",
                "UINTEGER" or "UBIGINT" => "bigint",
                "HUGEINT" => "decimal(38, 0)",
                "DOUBLE" or "FLOAT8" => "float",
                "FLOAT" or "REAL" or "FLOAT4" => "real",
                "BOOLEAN" or "BOOL" => "bit",
                "DATE" => "date",
                "TIME" => "time(6)",
                "TIMESTAMP" or "DATETIME" => "datetime2(6)",
                "TIMESTAMP WITH TIME ZONE" or "TIMESTAMPTZ" => "datetime2(6)",
                "BLOB" or "BYTEA" => "varbinary(max)",
                _ => "varchar(max)",
            };
            if (upper is "HUGEINT") select = $"{quoted}::DECIMAL(38, 0)";
            if (upper is "TIMESTAMP WITH TIME ZONE" or "TIMESTAMPTZ") select = $"({quoted} AT TIME ZONE 'UTC')::TIMESTAMP";
            if (upper is "UTINYINT" or "USMALLINT" or "UINTEGER" or "UBIGINT") select = $"{quoted}::BIGINT";
        }

        return new Column(name, duckType, sqlType, select);
    }

    private static async Task<List<Column>> ColumnsAsync(DuckDBConnection duck, string schema, string table, CancellationToken ct)
    {
        var raw = new List<(string Name, string Type)>();
        await using (var command = duck.CreateCommand())
        {
            command.CommandText = $"""
                SELECT column_name, data_type
                FROM information_schema.columns
                WHERE table_schema = '{schema}' AND table_name = '{table}'
                ORDER BY ordinal_position
                """;
            await using var reader = await command.ExecuteReaderAsync(ct);
            while (await reader.ReadAsync(ct)) raw.Add((reader.GetString(0), reader.GetString(1)));
        }

        if (raw.Count == 0) throw new InvalidOperationException($"The warehouse has no table {schema}.{table}.");

        // One pass for the longest value of every text column: the column can then be a varchar
        // that fits, instead of varchar(max).
        var text = raw.Where(c => c.Type.Equals("VARCHAR", StringComparison.OrdinalIgnoreCase)).Select(c => c.Name).ToList();
        var lengths = new Dictionary<string, int?>(StringComparer.Ordinal);
        if (text.Count > 0)
        {
            await using var command = duck.CreateCommand();
            command.CommandText = $"SELECT {string.Join(", ", text.Select(c => $"max(length(\"{c}\"))"))} FROM {schema}.{table}";
            await using var reader = await command.ExecuteReaderAsync(ct);
            if (await reader.ReadAsync(ct))
            {
                for (var i = 0; i < text.Count; i++)
                {
                    lengths[text[i]] = reader.IsDBNull(i) ? 1 : Convert.ToInt32(reader.GetValue(i), CultureInfo.InvariantCulture);
                }
            }
        }

        return raw.Select(c => MapColumn(c.Name, c.Type, lengths.GetValueOrDefault(c.Name))).ToList();
    }

    internal static string CreateTableSql(string schema, string table, IReadOnlyList<Column> columns) =>
        $"CREATE TABLE [{schema}].[{table}] (\n  " +
        string.Join(",\n  ", columns.Select(c => $"[{c.Name}] {c.SqlType} NULL")) +
        "\n)";

    internal static string SelectSql(string schema, string table, IReadOnlyList<Column> columns) =>
        $"SELECT {string.Join(", ", columns.Select(c => c.Select))} FROM {schema}.{table}";

    // ── copy ──────────────────────────────────────────────────────────────────

    private async Task CopyTableAsync(DuckDBConnection duck, SqlConnection sql, string duckSchema, string sqlSchema, string table, int batchSize, CancellationToken ct)
    {
        var clock = System.Diagnostics.Stopwatch.StartNew();
        var columns = await ColumnsAsync(duck, duckSchema, table, ct);

        await Execute(sql, $"DROP TABLE IF EXISTS [{sqlSchema}].[{table}]", ct);
        await Execute(sql, CreateTableSql(sqlSchema, table, columns), ct);

        await using var command = duck.CreateCommand();
        command.CommandText = SelectSql(duckSchema, table, columns);
        await using DbDataReader reader = await command.ExecuteReaderAsync(ct);

        using var bulk = new SqlBulkCopy(sql)
        {
            DestinationTableName = $"[{sqlSchema}].[{table}]",
            BatchSize = batchSize,
            BulkCopyTimeout = 0,
            EnableStreaming = true,
            NotifyAfter = 1_000_000,
        };
        for (var i = 0; i < columns.Count; i++) bulk.ColumnMappings.Add(i, columns[i].Name);
        bulk.SqlRowsCopied += (_, e) => log.LogInformation("  {Table}: {Rows:N0} rows", table, e.RowsCopied);

        await bulk.WriteToServerAsync(reader, ct);
        log.LogInformation("{Schema}.{Table} copied in {Seconds:N1} s", sqlSchema, table, clock.Elapsed.TotalSeconds);
    }

    // ── keys, indexes, columnstore ────────────────────────────────────────────

    private async Task RunScriptAsync(SqlConnection sql, string resource, CancellationToken ct)
    {
        var text = Resource(resource);
        if (text is null)
        {
            log.LogWarning("No script {Resource} in the assembly.", resource);
            return;
        }

        var clock = System.Diagnostics.Stopwatch.StartNew();
        foreach (var batch in SplitBatches(text))
        {
            await Execute(sql, batch, ct);
        }
        log.LogInformation("{Resource} in {Seconds:N1} s", resource, clock.Elapsed.TotalSeconds);
    }

    /// <summary>T-SQL scripts are split on a line that is only GO, the way sqlcmd does it.</summary>
    internal static IEnumerable<string> SplitBatches(string script)
    {
        var batch = new StringBuilder();
        foreach (var line in script.Split('\n'))
        {
            if (line.Trim().Equals("GO", StringComparison.OrdinalIgnoreCase))
            {
                if (batch.ToString().Trim().Length > 0) yield return batch.ToString();
                batch.Clear();
                continue;
            }
            batch.AppendLine(line);
        }
        if (batch.ToString().Trim().Length > 0) yield return batch.ToString();
    }

    // ── verify ────────────────────────────────────────────────────────────────

    private async Task<bool> VerifyAsync(DuckDBConnection duck, SqlConnection sql, IReadOnlyList<(string Model, string DuckSchema, string SqlSchema, string Table)> tables, CancellationToken ct)
    {
        var same = true;
        foreach (var (_, duckSchema, sqlSchema, table) in tables)
        {
            var expected = await CountAsync(duck, $"SELECT count(*) FROM {duckSchema}.{table}", ct);
            long actual;
            try
            {
                actual = await CountAsync(sql, $"SELECT count_big(*) FROM [{sqlSchema}].[{table}]", ct);
            }
            catch (SqlException e)
            {
                log.LogError("{Schema}.{Table}: {Message}", sqlSchema, table, e.Message);
                same = false;
                continue;
            }

            if (expected == actual)
            {
                log.LogInformation("{Schema}.{Table}: {Rows:N0} rows", sqlSchema, table, actual);
            }
            else
            {
                log.LogError("{Schema}.{Table}: {Actual:N0} rows in SQL Server, {Expected:N0} in the warehouse", sqlSchema, table, actual, expected);
                same = false;
            }
        }
        return same;
    }

    // ── plumbing ──────────────────────────────────────────────────────────────

    private async Task EnsureDatabaseAsync(SqlConnectionStringBuilder builder, string database, CancellationToken ct)
    {
        var master = new SqlConnectionStringBuilder(builder.ConnectionString) { InitialCatalog = "master" };
        await using var connection = new SqlConnection(master.ConnectionString);
        await connection.OpenAsync(ct);
        await Execute(connection, $"IF DB_ID('{database}') IS NULL CREATE DATABASE [{database}]", ct);
        // SIMPLE recovery: this is a copy that is rebuilt, and a bulk load must not fill the log.
        await Execute(connection, $"ALTER DATABASE [{database}] SET RECOVERY SIMPLE", ct);
        builder.InitialCatalog = database;
        log.LogInformation("Database {Database} on {Server}", database, builder.DataSource);
    }

    private static async Task Execute(SqlConnection connection, string sql, CancellationToken ct)
    {
        await using var command = connection.CreateCommand();
        command.CommandText = sql;
        command.CommandTimeout = 0;
        await command.ExecuteNonQueryAsync(ct);
    }

    private static async Task<long> CountAsync(DbConnection connection, string sql, CancellationToken ct)
    {
        await using var command = connection.CreateCommand();
        command.CommandText = sql;
        command.CommandTimeout = 0;
        return Convert.ToInt64(await command.ExecuteScalarAsync(ct), CultureInfo.InvariantCulture);
    }

    private static string? Resource(string name)
    {
        var stream = typeof(SqlServerLoader).Assembly.GetManifestResourceStream(name);
        if (stream is null) return null;
        using var reader = new StreamReader(stream);
        return reader.ReadToEnd();
    }

    private void Usage() => log.LogInformation(
        """
        Usage: dotnet DuckStore.Analytics.dll mssql-load [options]
          --connection CS     SQL Server connection string (default: ConnectionStrings:SqlServer)
          --models store,star which schemas to load (default: both)
          --tables a,b        only these tables
          --batch-size N      rows per bulk-copy batch (default: 50,000)
          --no-columnstore    star facts as rowstore, to measure what columnstore is worth
          --verify            only compare row counts with the warehouse
        """);
}
