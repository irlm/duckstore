namespace DuckStore.Contracts;

public enum AnalyticKind
{
    /// <summary>Scans and aggregates millions of rows, returns a small result.</summary>
    Analytics,

    /// <summary>Returns many rows: the network and JSON cost become visible.</summary>
    BigResult,

    /// <summary>A few rows found by key: what an application does on every page.</summary>
    Lookup,
}

public sealed record AnalyticDefinition(string Id, string Title, string Question, string Explanation, AnalyticKind Kind, bool NeedsCustomer = false);

// The questions of the Compare page. Each one has two SQL files in the top-level
// analytics/ folder: <id>.postgres.sql (normalized store tables, run by the web
// app) and <id>.duckdb.sql (star schema, run by the analytics service). Both
// return the same columns, so the page can check that the results match.
public static class AnalyticCatalog
{
    public static IReadOnlyList<AnalyticDefinition> All { get; } =
    [
        new("monthly-revenue", "Revenue per month",
            "Every order line converted to USD with the exchange rate of its day, summed per month.",
            "Postgres joins 3 tables and a daily exchange-rate calendar it builds on the fly (it has no ASOF JOIN). DuckDB sums one pre-converted column of the fact table.",
            AnalyticKind.Analytics),
        new("category-rollup", "Revenue by category with subtotals",
            "Walk the category tree and add subtotals per department and a grand total (GROUP BY ROLLUP).",
            "Postgres needs a recursive CTE over the tree plus the currency join; the star schema already has the tree flattened into columns.",
            AnalyticKind.Analytics),
        new("active-customers", "Active customers per month",
            "COUNT(DISTINCT customer_id) per month over every order.",
            "Distinct counts must remember every value. Postgres sorts inside each group; DuckDB uses parallel hash tables on one column.",
            AnalyticKind.Analytics),
        new("cohort-retention", "Cohort retention",
            "For customers who first bought in month X, how many bought again N months later (up to 12)?",
            "Two passes over all orders with DISTINCT and a join between them.",
            AnalyticKind.Analytics),
        new("rfm-segments", "RFM customer segments",
            "Score every customer on recency and frequency with NTILE(5) and group them into segments.",
            "One aggregation per customer, then two window functions sorting all customers.",
            AnalyticKind.Analytics),
        new("top-products-per-department", "Top 3 products per department (12 months)",
            "Rank products by revenue inside each department and keep the top 3.",
            "Postgres ranks in a subquery with ROW_NUMBER(); DuckDB filters the window function with QUALIFY.",
            AnalyticKind.Analytics),
        new("basket-pairs", "Products bought together (90 days)",
            "Pairs of products that appear in the same order, most frequent first.",
            "A self-join of order lines on order_id: the number of pairs grows with the square of the lines per order.",
            AnalyticKind.Analytics),
        new("brand-returns", "Return rate by brand",
            "Units sold vs units returned for every brand, over delivered orders.",
            "A multi-table join with a LEFT JOIN on the composite key (order_id, line_no).",
            AnalyticKind.Analytics),
        new("delivery-percentiles", "Delivery time percentiles by carrier",
            "Median and 90th percentile days in transit, and the share of parcels over 7 days.",
            "Percentiles need the sorted values of each group: PERCENTILE_CONT in Postgres, quantile_cont in DuckDB.",
            AnalyticKind.Analytics),
        new("sales-hierarchy", "Revenue by sales leader (org hierarchy)",
            "Revenue of business and VIP customers in 12 months, rolled up to every director and manager above the account executive.",
            "Postgres walks the org chart with a recursive CTE; the warehouse stored each manager chain as a list at ETL time.",
            AnalyticKind.Analytics),
        new("unusual-days", "Unusual days",
            "Days with far fewer or far more orders than the 7 days before (outages, Black Friday).",
            "A moving average with a window frame over daily counts.",
            AnalyticKind.Analytics),
        new("yoy-growth", "Year-over-year growth by department",
            "Revenue of each department for the last 12 complete months, next to the same month one year earlier.",
            "Monthly totals joined to themselves shifted by 12 months.",
            AnalyticKind.Analytics),
        new("clv-buckets", "Customer lifetime value distribution",
            "How many customers spent under $100, $100-250, ... over their whole history, and how much revenue each group brings.",
            "One aggregation per customer, then a second aggregation into buckets with a window total.",
            AnalyticKind.Analytics),
        new("detail-extract", "All order lines of one day (large result)",
            "Every order line of the latest complete day, with product names and amounts.",
            "The query itself is easy; moving tens of thousands of rows is not. Watch the transfer and JSON phases.",
            AnalyticKind.BigResult),
        new("customer-orders", "One customer's latest orders (lookup)",
            "The 20 latest orders of one customer, as an 'order history' page would show them.",
            "Postgres follows the index on orders (customer_id, placed_at). The DuckDB path adds an HTTP hop and scans the customer_id column.",
            AnalyticKind.Lookup, NeedsCustomer: true),
    ];

    public static AnalyticDefinition? Find(string id) => All.FirstOrDefault(a => a.Id == id);
}

/// <summary>
/// Parameters for both engines. MaxOrderId is the warehouse watermark: Postgres
/// only counts orders up to it, so both engines answer on the same orders.
/// </summary>
public sealed record AnalyticRequest(long MaxOrderId, long? CustomerId = null);

public sealed record AnalyticResult
{
    public required string AnalyticId { get; init; }
    public required string Engine { get; init; }
    public required string Sql { get; init; }
    public required IReadOnlyList<string> Columns { get; init; }

    /// <summary>Normalized cells (see <see cref="Cells"/>).</summary>
    public required IReadOnlyList<object?[]> Rows { get; init; }

    public long RowCount { get; init; }
    public bool Truncated { get; init; }

    /// <summary>From sending the query until the first row is available.</summary>
    public double ExecuteMs { get; init; }

    /// <summary>Reading the remaining rows and converting their values.</summary>
    public double ReadRowsMs { get; init; }
}

public sealed record AnalyticSql(string Id, string Sql);

/// <summary>
/// A query plan as text. Analyze = false: the plan the engine chose, with estimated rows (the query does not run).
/// Analyze = true: the query really ran, and the plan shows the actual rows and time of every step.
/// </summary>
public sealed record AnalyticPlan(string AnalyticId, string Engine, bool Analyze, string Text, double ElapsedMs);
