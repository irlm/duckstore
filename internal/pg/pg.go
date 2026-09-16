// Package pg connects to the store's Postgres database and manages its schema.
package pg

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema/*.sql
var schemaFS embed.FS

// Connect opens a connection pool with search_path=store, so queries can say
// "orders" instead of "store.orders". tracer (optional) sees every query.
func Connect(ctx context.Context, url string, tracer pgx.QueryTracer) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse postgres url: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "store"
	cfg.ConnConfig.RuntimeParams["application_name"] = "duckstore"
	cfg.MaxConns = 32
	cfg.ConnConfig.Tracer = tracer

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to postgres (is `make up` running?): %w", err)
	}
	return pool, nil
}

// ApplySchemaFile runs one embedded schema file (e.g. "01_tables.sql").
// Without query arguments pgx uses the simple protocol, which accepts many
// statements in one call, like a SQL Server batch.
func ApplySchemaFile(ctx context.Context, pool *pgxpool.Pool, name string) error {
	sql, err := schemaFS.ReadFile("schema/" + name)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("apply %s: %w", name, err)
	}
	return nil
}
