using DuckStore.Contracts;

namespace DuckStore.Analytics.Etl;

// Runs the ETL in the background of the analytics service and remembers the
// current (or last) run, so the web app's ETL page can show its progress.
// POST /api/etl/runs, the schedule and the startup build all go through here.
public sealed class EtlService(WarehouseBuilder builder, IHostApplicationLifetime lifetime, ILogger<EtlService> log)
{
    private readonly Lock _gate = new();
    private Run? _run;

    /// <summary>Raised after every step and when a run ends. Handlers must be quick.</summary>
    public event Action? Changed;

    public EtlRunSnapshot? Current
    {
        get
        {
            lock (_gate)
            {
                return _run?.Snapshot();
            }
        }
    }

    /// <summary>Starts a run in the background and returns at once.</summary>
    public EtlRunSnapshot Start(string trigger)
    {
        var run = Begin(trigger);
        _ = Task.Run(() => ExecuteAsync(run, lifetime.ApplicationStopping));
        return run.Snapshot();
    }

    /// <summary>Runs and waits until the run ends (used by the schedule).</summary>
    public Task RunAsync(string trigger, CancellationToken ct) => ExecuteAsync(Begin(trigger), ct);

    private Run Begin(string trigger)
    {
        lock (_gate)
        {
            if (_run is { FinishedAt: null })
            {
                throw new EtlAlreadyRunningException();
            }
            _run = new Run(trigger, WarehouseBuilder.ExpectedSteps());
            return _run;
        }
    }

    private async Task ExecuteAsync(Run run, CancellationToken ct)
    {
        try
        {
            var progress = new InlineProgress(step =>
            {
                lock (_gate) run.Steps.Add(step);
                Changed?.Invoke();
            });
            var result = await builder.BuildAsync(progress, ct);
            lock (_gate) run.Result = result;
        }
        catch (Exception e)
        {
            // Also when another process (the CLI or the Go ETL) holds the lock.
            log.LogError(e, "ETL run {RunId} failed", run.Id);
            lock (_gate) run.Error = e.Message;
        }
        finally
        {
            lock (_gate) run.FinishedAt = DateTime.UtcNow;
            Changed?.Invoke();
        }
    }

    private sealed class Run(string trigger, int expectedSteps)
    {
        public Guid Id { get; } = Guid.NewGuid();
        public DateTime StartedAt { get; } = DateTime.UtcNow;
        public DateTime? FinishedAt { get; set; }
        public List<EtlStep> Steps { get; } = [];
        public EtlResult? Result { get; set; }
        public string? Error { get; set; }

        public EtlRunSnapshot Snapshot() =>
            new(Id, trigger, StartedAt, FinishedAt, FinishedAt is null, Steps.ToList(), expectedSteps, Result, Error);
    }

    // Progress<T> would post to the thread pool out of order; report steps as they happen.
    private sealed class InlineProgress(Action<EtlStep> report) : IProgress<EtlStep>
    {
        public void Report(EtlStep value) => report(value);
    }
}

// Optional: build the warehouse at startup when the file does not exist yet
// (Etl:RunOnStartup, handy for the first `docker compose up`), and run the ETL
// every Etl:ScheduleMinutes (0 = off). In production the schedule is often a
// cron job running `dotnet DuckStore.Analytics.dll etl` instead.
public sealed class EtlSchedule(EtlService etl, DuckStore.Analytics.Warehouse.WarehouseSettings settings, IConfiguration config, ILogger<EtlSchedule> log) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        if (config.GetValue("Etl:RunOnStartup", false) && !File.Exists(settings.WarehousePath))
        {
            log.LogInformation("No warehouse at {Path}: building it now", settings.WarehousePath);
            try
            {
                await etl.RunAsync("startup", stoppingToken);
            }
            catch (EtlAlreadyRunningException)
            {
                // someone else is building it
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                // For example Postgres is not seeded yet. Keep the service up: the ETL page can retry.
                log.LogError(e, "Startup ETL failed");
            }
        }

        var minutes = config.GetValue("Etl:ScheduleMinutes", 0);
        if (minutes <= 0)
        {
            return;
        }
        log.LogInformation("ETL scheduled every {Minutes} minutes", minutes);
        using var timer = new PeriodicTimer(TimeSpan.FromMinutes(minutes));
        while (await timer.WaitForNextTickAsync(stoppingToken))
        {
            try
            {
                await etl.RunAsync("schedule", stoppingToken);
            }
            catch (EtlAlreadyRunningException)
            {
                log.LogInformation("Scheduled ETL skipped: a run is already in progress");
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                log.LogError(e, "Scheduled ETL failed");
            }
        }
    }
}
