using DuckStore.Analytics.Etl;
using DuckStore.Analytics.Warehouse;
using Microsoft.AspNetCore.Diagnostics;
using Microsoft.AspNetCore.Mvc;

namespace DuckStore.Analytics.Api;

// Maps known exceptions to HTTP status codes with a ProblemDetails body.
public sealed class ErrorHandler(IProblemDetailsService problemDetails) : IExceptionHandler
{
    public async ValueTask<bool> TryHandleAsync(HttpContext http, Exception exception, CancellationToken ct)
    {
        (int status, string title) = exception switch
        {
            WarehouseNotBuiltException => (StatusCodes.Status503ServiceUnavailable, "Warehouse not built"),
            EtlAlreadyRunningException => (StatusCodes.Status409Conflict, "ETL already running"),
            KeyNotFoundException => (StatusCodes.Status404NotFound, "Not found"),
            _ => (0, ""),
        };
        if (status == 0)
        {
            return false;
        }
        http.Response.StatusCode = status;
        return await problemDetails.TryWriteAsync(new ProblemDetailsContext
        {
            HttpContext = http,
            ProblemDetails = new ProblemDetails { Status = status, Title = title, Detail = exception.Message },
            Exception = exception,
        });
    }
}
