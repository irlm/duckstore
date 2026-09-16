# Seed-only Go binary: no DuckDB, no CGO, so the final image is tiny.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/seed cmd/seed
COPY internal internal
# go build downloads only the modules this binary needs (not DuckDB).
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /seed ./cmd/seed

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /seed /seed
ENTRYPOINT ["/seed"]
