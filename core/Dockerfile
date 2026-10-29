# core-api image: .NET 10 LTS SDK builds, ASP.NET Core runtime runs, as the image's non-root `app` user.
# One image, two containers (core-lakeshore, core-prairie), each configured with its own FI_ID and database.

FROM mcr.microsoft.com/dotnet/sdk:10.0.403 AS build
WORKDIR /src
COPY CoreApi/CoreApi.csproj CoreApi/
RUN dotnet restore CoreApi/CoreApi.csproj
COPY CoreApi/ CoreApi/
RUN dotnet publish CoreApi/CoreApi.csproj -c Release -o /out --no-restore

FROM mcr.microsoft.com/dotnet/aspnet:10.0.14
WORKDIR /app
COPY --from=build /out ./
USER $APP_UID
EXPOSE 8080
ENTRYPOINT ["dotnet", "CoreApi.dll"]
