using DuckStore.Web.Services;
using DuckStore.Web.Warehouse;
using Microsoft.AspNetCore.Diagnostics;
using Microsoft.AspNetCore.Mvc;

namespace DuckStore.Web.Api;

// Maps service exceptions to HTTP status codes with a ProblemDetails body.
public sealed class ErrorHandler(IProblemDetailsService problemDetails) : IExceptionHandler
{
    public async ValueTask<bool> TryHandleAsync(HttpContext http, Exception exception, CancellationToken ct)
    {
        (int status, string title) = exception switch
        {
            NotFoundException => (StatusCodes.Status404NotFound, "Not found"),
            ConflictException => (StatusCodes.Status409Conflict, "Conflict"),
            InputException => (StatusCodes.Status400BadRequest, "Invalid input"),
            WarehouseNotBuiltException => (StatusCodes.Status503ServiceUnavailable, "Warehouse not built"),
            _ => (0, ""),
        };
        if (status == 0)
        {
            return false; // unexpected: let the default handler return 500
        }

        http.Response.StatusCode = status;
        var details = new ProblemDetails { Status = status, Title = title, Detail = exception.Message };
        if (exception is InputException input)
        {
            details.Extensions["errors"] = input.Errors
                .GroupBy(e => e.MemberNames.FirstOrDefault() ?? "")
                .ToDictionary(g => g.Key, g => g.Select(e => e.ErrorMessage).ToArray());
        }
        return await problemDetails.TryWriteAsync(new ProblemDetailsContext { HttpContext = http, ProblemDetails = details, Exception = exception });
    }
}
