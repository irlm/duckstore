using DuckStore.Web.Api;
using DuckStore.Web.Components;
using DuckStore.Web.Data;
using DuckStore.Web.Services;
using Microsoft.EntityFrameworkCore;
using MudBlazor;
using MudBlazor.Services;
using Scalar.AspNetCore;

// The web app: UI + CRUD on Postgres. Everything DuckDB lives in the analytics
// service (DuckStore.Analytics), which this app calls over HTTP.
var builder = WebApplication.CreateBuilder(args);

// Postgres (OLTP): EF Core, one short-lived DbContext per operation.
builder.Services.AddDbContextFactory<StoreDbContext>(options =>
    options.UseNpgsql(builder.Configuration.GetConnectionString("Store")));
builder.Services.AddSingleton<ProductService>();
builder.Services.AddSingleton<StoreQueries>();

// The analytics service (DuckDB), reached over HTTP.
builder.Services.AddHttpClient<AnalyticsApiClient>(client =>
{
    client.BaseAddress = new Uri(builder.Configuration["Analytics:BaseUrl"] ?? "http://127.0.0.1:5090/");
    client.Timeout = TimeSpan.FromMinutes(15);
});

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
app.MapGet("/health", () => Results.Ok(new { status = "ok" })).ExcludeFromDescription();
app.MapProductEndpoints();

app.MapStaticAssets();
app.MapRazorComponents<App>().AddInteractiveServerRenderMode();

app.Run();

// Makes the Program class visible to WebApplicationFactory in the test project.
public partial class Program;
