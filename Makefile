SCALE ?= 1
BIN   := bin/duckstore

.PHONY: build up down reset seed etl run bench test dotnet-run dotnet-analytics dotnet-etl dotnet-test docker-up docker-seed docker-etl docker-etl-incremental docker-star docker-indexes docker-indexes-drop docker-pgduckdb docker-loadtest docker-loadtest-separate docker-compare docker-logs dotnet-star

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

## docker-etl-incremental: load only what changed since the last ETL (etl/incremental/incremental.sql)
docker-etl-incremental:
	docker compose exec analytics dotnet DuckStore.Analytics.dll etl --incremental

## docker-star: copy the warehouse's star schema into Postgres (schema dw) for the Compare page's fourth approach
docker-star:
	docker compose run --rm --no-deps analytics star-to-postgres

## docker-indexes: add covering indexes for the Compare questions to the Postgres store tables (analytics/postgres-indexes.sql)
docker-indexes:
	docker compose exec -T postgres psql -U store -d store -v ON_ERROR_STOP=1 -f - < analytics/postgres-indexes.sql

## docker-indexes-drop: remove those indexes again, to measure without them
docker-indexes-drop:
	docker compose exec -T postgres psql -U store -d store -v ON_ERROR_STOP=1 -f - < analytics/postgres-indexes-drop.sql

## docker-pgduckdb: enable the pg_duckdb extension (DuckDB's engine inside Postgres) for the Compare page
docker-pgduckdb:
	docker compose exec -T postgres psql -U store -d store -v ON_ERROR_STOP=1 -f - < analytics/postgres-pg_duckdb.sql

## docker-loadtest: store traffic alone, with reports on Postgres, with reports on the DuckDB service (shared CPU)
docker-loadtest:
	docker compose exec web dotnet DuckStore.Web.dll loadtest $(ARGS)

# CPUs for docker-loadtest-separate (a 16-thread machine: 4 cores for Postgres, 3 for analytics, 1 for web + proxy).
PG_CPUS ?= 0-7
ANALYTICS_CPUS ?= 8-13
APP_CPUS ?= 14-15

## docker-loadtest-separate: the same, with Postgres and the analytics service on their own cores, like separate servers
docker-loadtest-separate:
	docker update --cpuset-cpus $(PG_CPUS) duckstore-postgres
	docker update --cpuset-cpus $(ANALYTICS_CPUS) duckstore-analytics
	docker update --cpuset-cpus $(APP_CPUS) duckstore-web duckstore-toxiproxy
	docker compose exec web dotnet DuckStore.Web.dll loadtest $(ARGS); status=$$?; \
	docker update --cpuset-cpus 0-$$(($$(nproc) - 1)) duckstore-postgres duckstore-analytics duckstore-web duckstore-toxiproxy; \
	exit $$status

## docker-compare: run every Compare question from the web container and print the timings
docker-compare:
	docker compose exec web dotnet DuckStore.Web.dll compare

## docker-logs: follow the logs of the analytics and web containers
docker-logs:
	docker compose logs -f analytics web

## docker-mssql: start SQL Server 2025 Developer (writes a password into .env the first time)
docker-mssql:
	@grep -q '^MSSQL_SA_PASSWORD=' .env 2>/dev/null || { \
	  umask 077; \
	  printf 'MSSQL_SA_PASSWORD=%s\n' "$$(openssl rand -base64 18 | tr -d '/+=')Aa1!" >> .env; \
	  echo "wrote a new MSSQL_SA_PASSWORD to .env"; }
	docker compose --profile mssql up -d --wait mssql

## docker-mssql-load: copy the warehouse into SQL Server (rowstore store tables, columnstore star)
docker-mssql-load:
	docker compose run --rm --no-deps analytics mssql-load $(ARGS)

## docker-mssql-down: stop SQL Server and free its memory
docker-mssql-down:
	docker compose --profile mssql stop mssql

## docker-duckdb-cli: build the DuckDB command line tool as an image, for machines without it
docker-duckdb-cli:
	docker build -f docker/duckdb.Dockerfile -t duckstore-duckdb .

## docker-export-sql: write the questions as ready-to-run SQL into bench/sql
docker-export-sql:
	docker compose run --rm --no-deps analytics export-sql --out /data/bench-sql --engines postgres,duckdb,mssql
	rm -rf bench/sql && mkdir -p bench/sql
	docker cp duckstore-analytics:/data/bench-sql/. bench/sql/

## bench-list: what engines this machine can benchmark right now, and why not the others
bench-list:
	bash bench/duckstore-bench-local.sh --list

## bench-local: run the benchmark on every engine this machine can run (ARGS="--yes --repeat 5")
bench-local:
	bash bench/duckstore-bench-local.sh $(ARGS)

## bench-verify: do two engines answer the same? (ARGS="--engine mssql --model store")
bench-verify:
	bash bench/duckstore-bench-verify.sh $(ARGS)
