SCALE ?= 1
BIN   := bin/duckstore

.PHONY: build up down reset seed etl run bench test dotnet-run dotnet-etl dotnet-test

## build: compile the duckstore binary (CGO is required by DuckDB)
build:
	CGO_ENABLED=1 go build -o $(BIN) ./cmd/duckstore

## up: start Postgres in Docker and wait until it is healthy
up:
	docker compose up -d --wait

## down: stop Postgres (data is kept in the volume)
down:
	docker compose down

## reset: stop Postgres and delete all data (Postgres volume + DuckDB files)
reset:
	docker compose down -v
	rm -rf data

## seed: create the store schema and load fake data (SCALE=1 is about 2M orders)
seed: build
	$(BIN) seed -scale $(SCALE)

## etl: copy Postgres into a new DuckDB warehouse file and swap it in
etl: build
	$(BIN) etl

## run: start the web app (store + analytics) on http://localhost:8080
run: build
	$(BIN) serve

## bench: run the Postgres vs DuckDB comparisons in the terminal
bench: build
	$(BIN) bench

## test: unit tests + integration tests (integration needs `make up`)
test:
	CGO_ENABLED=1 go test ./...

## dotnet-run: run the ASP.NET Core app (CRUD on Postgres, reports on DuckDB) on http://127.0.0.1:5085
dotnet-run:
	dotnet run --project dotnet/src/DuckStore.Web --launch-profile http

## dotnet-etl: build the DuckDB warehouse with the C# ETL (same SQL files as `make etl`)
dotnet-etl:
	dotnet run --project dotnet/src/DuckStore.Web --launch-profile http -- etl

## dotnet-test: integration tests of the .NET app (needs `make up seed etl`)
dotnet-test:
	dotnet test dotnet/DuckStore.slnx
