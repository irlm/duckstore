namespace DuckStore.Contracts;

public sealed record EtlStep(int Number, string Label, long Rows, TimeSpan Duration);

public sealed record EtlResult(long MaxOrderId, IReadOnlyList<EtlStep> Steps, TimeSpan Duration, long SizeBytes, string WarehousePath);

public sealed record EtlRunSnapshot(
    Guid Id, string Trigger, DateTime StartedAt, DateTime? FinishedAt, bool IsRunning,
    IReadOnlyList<EtlStep> Steps, int ExpectedSteps, EtlResult? Result, string? Error, string Mode = "full");

/// <summary>One row of dw.etl_runs: every full or incremental load of the warehouse.</summary>
public sealed record EtlRunRecord(DateTime FinishedAt, string Mode, long FromOrderId, long ToOrderId, long ChangedOrders, double Seconds);
