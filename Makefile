SCALE ?= 1
BIN   := bin/duckstore

.PHONY: build up down reset seed etl run bench test dotnet-run dotnet-analytics dotnet-etl dotnet-test docker-up docker-seed docker-etl docker-star docker-indexes docker-indexes-drop docker-compare docker-logs dotnet-star

## build: compile the duckstore binary (CGO is required by DuckDB)
build:
	CGO_ENABLED=1 go build -o $(BIN) ./cmd/duckstore

## up: start only Postgres in Docker (for the Go app and local .NET runs)
up:
	docker compose up -d --wait postgres

## down: stop all containers (data is kept in the volumes)
down:
	docker compose down

## reset: stop all containers and delete all data (Docker volumes + local DuckDB files)
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

## dotnet-analytics: run the analytics service (DuckDB + ETL) on http://127.0.0.1:5090
dotnet-analytics:
	dotnet run --project dotnet/src/DuckStore.Analytics --launch-profile http

## dotnet-etl: build the DuckDB warehouse with the C# ETL (same SQL files as `make etl`)
dotnet-etl:
	dotnet run --project dotnet/src/DuckStore.Analytics --launch-profile http -- etl

## dotnet-star: copy the local warehouse's star schema into Postgres (schema dw)
dotnet-star:
	dotnet run --project dotnet/src/DuckStore.Analytics --launch-profile http -- star-to-postgres

## dotnet-test: integration tests of the .NET app (needs `make up seed etl`)
dotnet-test:
	dotnet test dotnet/DuckStore.slnx

## docker-up: build and start postgres, analytics and web in Docker; UI on http://127.0.0.1:5085
docker-up:
	docker compose up -d --build --wait

## docker-seed: load fake data into the Docker Postgres (default SCALE=5, ~12.5M orders), then rebuild the warehouse
docker-seed: SCALE = 5
docker-seed:
	docker compose up -d --wait postgres
	docker compose run --rm --build seed -scale $(SCALE)
	docker compose run --rm --build --no-deps analytics etl

## docker-etl: rebuild the warehouse in the analytics volume (the running service picks up the new file)
docker-etl:
	docker compose run --rm --no-deps analytics etl

## docker-star: copy the warehouse's star schema into Postgres (schema dw) for the Compare page's fourth approach
docker-star:
	docker compose run --rm --no-deps analytics star-to-postgres

## docker-indexes: add covering indexes for the Compare questions to the Postgres store tables (analytics/postgres-indexes.sql)
docker-indexes:
	docker compose exec -T postgres psql -U store -d store -v ON_ERROR_STOP=1 -f - < analytics/postgres-indexes.sql

## docker-indexes-drop: remove those indexes again, to measure without them
docker-indexes-drop:
	docker compose exec -T postgres psql -U store -d store -v ON_ERROR_STOP=1 -f - < analytics/postgres-indexes-drop.sql

## docker-compare: run every Compare question from the web container and print the timings
docker-compare:
	docker compose exec web dotnet DuckStore.Web.dll compare

## docker-logs: follow the logs of the analytics and web containers
docker-logs:
	docker compose logs -f analytics web
