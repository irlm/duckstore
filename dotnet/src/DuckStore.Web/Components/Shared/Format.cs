using System.Globalization;

namespace DuckStore.Web.Components.Shared;

public static class Format
{
    public static string Ms(double ms) => ms switch
    {
        < 1 => ms.ToString("0.00", CultureInfo.CurrentCulture) + " ms",
        < 100 => ms.ToString("0.0", CultureInfo.CurrentCulture) + " ms",
        < 1000 => ms.ToString("0", CultureInfo.CurrentCulture) + " ms",
        _ => (ms / 1000).ToString("0.00", CultureInfo.CurrentCulture) + " s",
    };

    public static string Bytes(long bytes) => bytes switch
    {
        < 1024 => $"{bytes} B",
        < 1024 * 1024 => $"{bytes / 1024.0:0.0} KB",
        _ => $"{bytes / 1024.0 / 1024.0:0.0} MB",
    };

    public static string Cell(object? value) => value switch
    {
        null => "NULL",
        long l => l.ToString("N0", CultureInfo.CurrentCulture),
        decimal d => d.ToString("N2", CultureInfo.CurrentCulture),
        double d when Math.Abs(d % 1) < 1e-9 && Math.Abs(d) < 1e15 => d.ToString("N0", CultureInfo.CurrentCulture),
        double d => d.ToString("N2", CultureInfo.CurrentCulture),
        _ => Convert.ToString(value, CultureInfo.CurrentCulture) ?? "",
    };

    public static bool IsNumber(object? value) => value is long or decimal or double;
}
