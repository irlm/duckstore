namespace DuckStore.Contracts;

public sealed record EtlStep(int Number, string Label, long Rows, TimeSpan Duration);

public sealed record EtlResult(long MaxOrderId, IReadOnlyList<EtlStep> Steps, TimeSpan Duration, long SizeBytes, string WarehousePath);

public sealed record EtlRunSnapshot(
    Guid Id, string Trigger, DateTime StartedAt, DateTime? FinishedAt, bool IsRunning,
    IReadOnlyList<EtlStep> Steps, int ExpectedSteps, EtlResult? Result, string? Error);
