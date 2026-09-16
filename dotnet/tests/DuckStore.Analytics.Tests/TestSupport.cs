using System.Net.Sockets;

namespace DuckStore.Analytics.Tests;

// A [Fact] that is skipped when Postgres (and, if asked, the warehouse file) is missing.
public sealed class DatabasesFactAttribute : FactAttribute
{
    public DatabasesFactAttribute(bool needsWarehouse = false)
    {
        if (!PostgresIsUp())
        {
            Skip = "Postgres is not reachable on 127.0.0.1:55432. Run `make up && make seed`.";
        }
        else if (needsWarehouse && !File.Exists(Repository.WarehousePath))
        {
            Skip = "The DuckDB warehouse is not built. Run the ETL first.";
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

public static class Repository
{
    // The repository root is the folder that has docker-compose.yml.
    public static string Root
    {
        get
        {
            var dir = new DirectoryInfo(AppContext.BaseDirectory);
            while (dir is not null && !File.Exists(Path.Combine(dir.FullName, "docker-compose.yml")))
            {
                dir = dir.Parent;
            }
            return dir?.FullName ?? "";
        }
    }

    public static string WarehousePath => Path.Combine(Root, "data", "warehouse.duckdb");
}
