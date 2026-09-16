using System.Net;
using System.Net.Http.Json;
using System.Text.Json;
using DuckStore.Contracts;

namespace DuckStore.Web.Services;

// Typed HttpClient for the analytics service (the DuckDB container).
public sealed class AnalyticsApiClient(HttpClient http)
{
    public Uri? BaseAddress => http.BaseAddress;

    public Task<WarehouseInfo> WarehouseAsync(CancellationToken ct = default) => GetAsync<WarehouseInfo>("api/warehouse", ct);

    public Task<Report<KpiPeriod>> KpisAsync(CancellationToken ct = default) => GetAsync<Report<KpiPeriod>>("api/reports/kpis", ct);

    public Task<Report<MonthlyRevenue>> MonthlyRevenueAsync(CancellationToken ct = default) => GetAsync<Report<MonthlyRevenue>>("api/reports/monthly-revenue", ct);

    public Task<Report<TopProduct>> TopProductsAsync(int days, CancellationToken ct = default) => GetAsync<Report<TopProduct>>($"api/reports/top-products?days={days}", ct);

    public Task<Report<ReturnRate>> ReturnRatesAsync(CancellationToken ct = default) => GetAsync<Report<ReturnRate>>("api/reports/return-rates", ct);

    public Task<Report<CountrySales>> SalesByCountryAsync(CancellationToken ct = default) => GetAsync<Report<CountrySales>>("api/reports/sales-by-country", ct);

    public Task<Report<BuildStep>> LastBuildStepsAsync(CancellationToken ct = default) => GetAsync<Report<BuildStep>>("api/reports/last-build-steps", ct);

    public async Task<TableReport> RevenueByDepartmentAndYearAsync(CancellationToken ct = default)
    {
        var report = await GetAsync<TableReport>("api/reports/revenue-by-department-year", ct);
        // Cells arrive as JsonElement; turn them back into numbers and text.
        var rows = report.Rows.Select(r => r.Select(c => c is JsonElement e ? Cells.FromJson(e) : c).ToArray()).ToList();
        return report with { Rows = rows };
    }

    public async Task<EtlRunSnapshot> StartEtlAsync(CancellationToken ct = default)
    {
        using var response = await http.PostAsync("api/etl/runs", content: null, ct);
        await EnsureSuccessAsync(response, ct);
        return (await response.Content.ReadFromJsonAsync<EtlRunSnapshot>(ct))!;
    }

    public async Task<EtlRunSnapshot?> EtlStatusAsync(CancellationToken ct = default)
    {
        using var response = await http.GetAsync("api/etl/status", ct);
        if (response.StatusCode == HttpStatusCode.NoContent)
        {
            return null;
        }
        await EnsureSuccessAsync(response, ct);
        return await response.Content.ReadFromJsonAsync<EtlRunSnapshot>(ct);
    }

    private async Task<T> GetAsync<T>(string path, CancellationToken ct)
    {
        using var response = await http.GetAsync(path, ct);
        await EnsureSuccessAsync(response, ct);
        return (await response.Content.ReadFromJsonAsync<T>(ct))!;
    }

    private static async Task EnsureSuccessAsync(HttpResponseMessage response, CancellationToken ct)
    {
        if (response.IsSuccessStatusCode)
        {
            return;
        }
        string? detail = null;
        try
        {
            var problem = await response.Content.ReadFromJsonAsync<JsonElement>(ct);
            detail = problem.TryGetProperty("detail", out var d) ? d.GetString() : null;
        }
        catch (JsonException)
        {
        }
        throw new AnalyticsServiceException(detail ?? $"The analytics service answered {(int)response.StatusCode} {response.ReasonPhrase}.", response.StatusCode);
    }
}

public sealed class AnalyticsServiceException(string message, HttpStatusCode? status = null) : Exception(message)
{
    public HttpStatusCode? Status { get; } = status;
}
