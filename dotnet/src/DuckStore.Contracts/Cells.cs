using System.Collections;
using System.Globalization;
using System.Numerics;
using System.Text.Json;

namespace DuckStore.Contracts;

// One representation for result values from both engines, so they can travel as
// JSON and be compared: integers -> long, other numbers -> decimal or double,
// dates -> "yyyy-MM-dd", timestamps -> "yyyy-MM-dd HH:mm:ss", lists -> text.
public static class Cells
{
    public static object? Normalize(object? value) => value switch
    {
        null or DBNull => null,
        string s => s,
        bool b => b,
        byte or sbyte or short or ushort or int or uint or long => Convert.ToInt64(value, CultureInfo.InvariantCulture),
        ulong u => u <= long.MaxValue ? (long)u : (double)u,
        BigInteger big => big >= long.MinValue && big <= long.MaxValue ? (long)big : (double)big, // DuckDB HUGEINT
        decimal d => d,
        float f => (double)f,
        double d => d,
        DateOnly d => d.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture),
        DateTime t => t.TimeOfDay == TimeSpan.Zero
            ? t.ToString("yyyy-MM-dd", CultureInfo.InvariantCulture)
            : t.ToString("yyyy-MM-dd HH:mm:ss", CultureInfo.InvariantCulture),
        DateTimeOffset t => Normalize(t.UtcDateTime),
        TimeSpan span => span.ToString("c", CultureInfo.InvariantCulture),
        IEnumerable list => string.Join(", ", list.Cast<object?>().Select(x => Normalize(x)?.ToString())),
        _ => Convert.ToString(value, CultureInfo.InvariantCulture),
    };

    /// <summary>A value read back from JSON (the analytics service response).</summary>
    public static object? FromJson(JsonElement e) => e.ValueKind switch
    {
        JsonValueKind.Number when e.TryGetInt64(out var l) => l,
        JsonValueKind.Number => e.GetDouble(),
        JsonValueKind.String => e.GetString(),
        JsonValueKind.True => true,
        JsonValueKind.False => false,
        _ => null,
    };

    public static double? AsNumber(object? value) => value switch
    {
        long l => l,
        int i => i,
        decimal d => (double)d,
        double d => d,
        _ => null,
    };

    /// <summary>
    /// Equal, allowing tiny differences in computed decimals: Postgres rounds exact
    /// numerics and DuckDB rounds doubles, so money sums can differ by a few cents.
    /// </summary>
    public static bool Same(object? a, object? b)
    {
        if (a is null || b is null)
        {
            return a is null && b is null;
        }
        if (AsNumber(a) is double x && AsNumber(b) is double y)
        {
            var tolerance = Math.Max(0.011, 1e-6 * Math.Max(Math.Abs(x), Math.Abs(y)));
            return Math.Abs(x - y) <= tolerance;
        }
        return string.Equals(Convert.ToString(a, CultureInfo.InvariantCulture), Convert.ToString(b, CultureInfo.InvariantCulture), StringComparison.Ordinal);
    }
}
