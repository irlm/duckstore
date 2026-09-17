using System.Net.Http.Json;
using System.Text.Json;
using Npgsql;

namespace DuckStore.Web.Services;

/// <summary>How the Compare page reaches Postgres and the analytics service.</summary>
/// <param name="LatencyMs">null = direct connections; 0 or more = through Toxiproxy, which adds this delay in each direction.</param>
public sealed record NetworkSetting(int? LatencyMs)
{
    public static NetworkSetting Direct { get; } = new((int?)null);

    public bool ViaProxy => LatencyMs is not null;

    public string Label => LatencyMs switch
    {
        null => "direct",
        0 => "proxy, no delay",
        var ms => $"+{ms} ms each way",
    };

    /// <summary>"direct", "0", "5": the form used by the CLI and the CSV.</summary>
    public string Code => LatencyMs?.ToString(System.Globalization.CultureInfo.InvariantCulture) ?? "direct";

    public static NetworkSetting Parse(string code) =>
        code == "direct" ? Direct : new(int.Parse(code, System.Globalization.CultureInfo.InvariantCulture));
}

// A network you can slow down on purpose. Toxiproxy (docker-compose service "toxiproxy") forwards
// TCP connections and can add "toxics" to them:
//
//   web app --> toxiproxy:15432 --> postgres:5432
//   web app --> toxiproxy:18080 --> analytics:8080
//
// A "latency" toxic delays every piece of data by N ms in one direction. The lab adds it to both
// directions, so one request and its response cost about 2 × N ms more, like a round trip over a
// real network (1 ms ≈ same data center, 5 ms ≈ same city, 25 ms ≈ another region).
public sealed class NetworkLab(IHttpClientFactory httpFactory, IConfiguration config) : IAsyncDisposable
{
    public const string ProxyHttpClient = "analytics-via-proxy";

    private readonly Lazy<NpgsqlDataSource?> _postgresViaProxy = new(() =>
        config.GetConnectionString("StoreViaProxy") is { Length: > 0 } cs ? NpgsqlDataSource.Create(cs) : null);

    /// <summary>False when the web app runs without the toxiproxy container (for example with `make dotnet-run`).</summary>
    public bool Configured => config["Network:ToxiproxyUrl"] is { Length: > 0 } && _postgresViaProxy.Value is not null;

    public NpgsqlDataSource PostgresViaProxy => _postgresViaProxy.Value ?? throw new InvalidOperationException("ConnectionStrings:StoreViaProxy is not set.");

    public AnalyticsApiClient AnalyticsViaProxy() => new(httpFactory.CreateClient(ProxyHttpClient));

    public async Task<bool> IsReachableAsync(CancellationToken ct = default)
    {
        if (!Configured) return false;
        try
        {
            using var response = await Toxiproxy().GetAsync("version", ct);
            return response.IsSuccessStatusCode;
        }
        catch (HttpRequestException)
        {
            return false;
        }
    }

    /// <summary>Creates the two proxies if needed and sets their delay. Direct connections need nothing.</summary>
    /// <remarks>
    /// Only what differs is changed: recreating a proxy (or re-sending one whose settings look different,
    /// like "0.0.0.0:15432" vs the "[::]:15432" Toxiproxy reports) closes its open connections, and pooled
    /// Postgres connections would then fail with "Exception while reading from stream".
    /// </remarks>
    public async Task ApplyAsync(NetworkSetting setting, CancellationToken ct = default)
    {
        if (setting.LatencyMs is not { } ms) return;
        var toxiproxy = Toxiproxy();

        var proxies = await toxiproxy.GetFromJsonAsync<Dictionary<string, JsonElement>>("proxies", ct) ?? [];
        var missing = new[]
        {
            new { name = "postgres", listen = "0.0.0.0:15432", upstream = config["Network:PostgresUpstream"] ?? "postgres:5432", enabled = true },
            new { name = "analytics", listen = "0.0.0.0:18080", upstream = config["Network:AnalyticsUpstream"] ?? "analytics:8080", enabled = true },
        }.Where(p => !proxies.ContainsKey(p.name)).ToArray();
        if (missing.Length > 0)
        {
            using var created = await toxiproxy.PostAsJsonAsync("populate", missing, ct);
            created.EnsureSuccessStatusCode();
            proxies = await toxiproxy.GetFromJsonAsync<Dictionary<string, JsonElement>>("proxies", ct) ?? [];
        }

        foreach (var (proxy, state) in proxies.Where(p => p.Key is "postgres" or "analytics"))
        {
            var current = state.GetProperty("toxics").EnumerateArray()
                .ToDictionary(t => t.GetProperty("name").GetString()!, t => t.GetProperty("attributes").GetProperty("latency").GetInt32());
            foreach (var stream in new[] { "upstream", "downstream" })
            {
                var name = $"latency_{stream}";
                var body = new { name, type = "latency", stream, toxicity = 1.0, attributes = new { latency = ms, jitter = 0 } };
                using var response = (current.TryGetValue(name, out var latency), ms) switch
                {
                    (true, 0) => await toxiproxy.DeleteAsync($"proxies/{proxy}/toxics/{name}", ct),
                    (true, _) when latency == ms => null,
                    (true, _) => await toxiproxy.PostAsJsonAsync($"proxies/{proxy}/toxics/{name}", body, ct), // update
                    (false, 0) => null,
                    (false, _) => await toxiproxy.PostAsJsonAsync($"proxies/{proxy}/toxics", body, ct),     // create
                };
                response?.EnsureSuccessStatusCode();
            }
        }
    }

    private HttpClient Toxiproxy()
    {
        var client = httpFactory.CreateClient("toxiproxy");
        client.BaseAddress = new Uri(config["Network:ToxiproxyUrl"]!);
        return client;
    }

    public async ValueTask DisposeAsync()
    {
        if (_postgresViaProxy.IsValueCreated && _postgresViaProxy.Value is { } source)
        {
            await source.DisposeAsync();
        }
    }
}
