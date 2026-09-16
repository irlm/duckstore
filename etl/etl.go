// Package etl holds the ETL as plain SQL files, run in name order
// (01_extract.sql, 02_dimensions.sql, ...).
//
// The SQL is the asset; the host language only runs it. Both the Go version
// (internal/warehouse/etl.go) and the .NET version
// (dotnet/src/DuckStore.Web/Etl/WarehouseBuilder.cs) execute these same files.
//
// Conventions the runners rely on:
//   - statements end with ';'
//   - a "-- step: <label>" comment before a statement names it in the progress log
//   - {{MAX_ORDER_ID}} is replaced by the watermark (highest order id in Postgres)
//   - the Postgres store is attached as "pg" before the first file runs
package etl

import "embed"

//go:embed *.sql
var FS embed.FS
