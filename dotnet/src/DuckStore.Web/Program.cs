using DuckStore.Web.Api;
using DuckStore.Web.Components;
using DuckStore.Web.Data;
using DuckStore.Web.Services;
using DuckStore.Web.Warehouse;
using Microsoft.EntityFrameworkCore;
using MudBlazor;
using MudBlazor.Services;
using Scalar.AspNetCore;

var builder = WebApplication.CreateBuilder(args);

// Postgres (OLTP): EF Core, one short-lived DbContext per operation.
builder.Services.AddDbContextFactory<StoreDbContext>(options =>
    options.UseNpgsql(builder.Configuration.GetConnectionString("Store")));
builder.Services.AddSingleton<ProductService>();

// DuckDB (OLAP): the warehouse file built by `make etl`, read-only, with Dapper.
builder.Services.AddSingleton<DuckDbWarehouse>();
builder.Services.AddSingleton<ReportService>();

// API: OpenAPI document + Scalar UI at /scalar, errors as ProblemDetails.
builder.Services.AddOpenApi();
builder.Services.AddProblemDetails();
builder.Services.AddExceptionHandler<ErrorHandler>();

// UI: Blazor (interactive server rendering) + MudBlazor components.
builder.Services.AddRazorComponents().AddInteractiveServerComponents();
builder.Services.AddMudServices(options => options.SnackbarConfiguration.PositionClass = Defaults.Classes.Position.BottomRight);

var app = builder.Build();

app.UseExceptionHandler();
app.UseStatusCodePagesWithReExecute("/not-found", createScopeForStatusCodePages: true);
app.UseAntiforgery();

app.MapOpenApi();
app.MapScalarApiReference();
app.MapProductEndpoints();
app.MapReportEndpoints();

app.MapStaticAssets();
app.MapRazorComponents<App>().AddInteractiveServerRenderMode();

app.Run();
