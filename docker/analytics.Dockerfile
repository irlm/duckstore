# The analytics service: DuckDB warehouse + ETL + analytics API.
FROM mcr.microsoft.com/dotnet/sdk:10.0 AS build
ARG TARGETARCH
WORKDIR /src
COPY dotnet/src/DuckStore.Contracts/DuckStore.Contracts.csproj dotnet/src/DuckStore.Contracts/
COPY dotnet/src/DuckStore.Analytics/DuckStore.Analytics.csproj dotnet/src/DuckStore.Analytics/
RUN dotnet restore dotnet/src/DuckStore.Analytics -a $TARGETARCH
COPY dotnet/src/DuckStore.Contracts dotnet/src/DuckStore.Contracts
COPY dotnet/src/DuckStore.Analytics dotnet/src/DuckStore.Analytics
# The SQL files are embedded into the assembly from these top-level folders.
COPY etl etl
COPY analytics analytics
RUN dotnet publish dotnet/src/DuckStore.Analytics -c Release -a $TARGETARCH --no-restore -o /app

FROM mcr.microsoft.com/dotnet/aspnet:10.0
WORKDIR /app
COPY --from=build /app .
ENV Warehouse__Path=/data/warehouse.duckdb \
    Warehouse__ExtensionDirectory=/app/duckdb-extensions
# Download DuckDB's postgres extension now, so the container needs no internet to run the ETL.
RUN dotnet DuckStore.Analytics.dll install-extensions \
 && mkdir /data && chown $APP_UID /data
USER $APP_UID
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["dotnet", "DuckStore.Analytics.dll"]
