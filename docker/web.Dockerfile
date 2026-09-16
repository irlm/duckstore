# The web app: Blazor UI, product CRUD on Postgres, Compare page.
FROM mcr.microsoft.com/dotnet/sdk:10.0 AS build
ARG TARGETARCH
WORKDIR /src
COPY dotnet/src/DuckStore.Contracts/DuckStore.Contracts.csproj dotnet/src/DuckStore.Contracts/
COPY dotnet/src/DuckStore.Web/DuckStore.Web.csproj dotnet/src/DuckStore.Web/
RUN dotnet restore dotnet/src/DuckStore.Web -a $TARGETARCH
COPY dotnet/src/DuckStore.Contracts dotnet/src/DuckStore.Contracts
COPY dotnet/src/DuckStore.Web dotnet/src/DuckStore.Web
# The Postgres side of the Compare page is embedded from here.
COPY analytics analytics
RUN dotnet publish dotnet/src/DuckStore.Web -c Release -a $TARGETARCH --no-restore -o /app

FROM mcr.microsoft.com/dotnet/aspnet:10.0
WORKDIR /app
COPY --from=build /app .
USER $APP_UID
EXPOSE 8080
ENTRYPOINT ["dotnet", "DuckStore.Web.dll"]
