// Package config reads settings from environment variables with local defaults.
package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	PostgresURL string // DUCKSTORE_PG_URL
	DataDir     string // DUCKSTORE_DATA_DIR: DuckDB files and Parquet exports
	Listen      string // DUCKSTORE_LISTEN
}

func Load() Config {
	return Config{
		PostgresURL: env("DUCKSTORE_PG_URL", "postgres://store:store@127.0.0.1:55432/store?sslmode=disable"),
		DataDir:     env("DUCKSTORE_DATA_DIR", "data"),
		Listen:      env("DUCKSTORE_LISTEN", "127.0.0.1:8080"),
	}
}

// WarehousePath is the DuckDB file the analytics side reads.
func (c Config) WarehousePath() string { return filepath.Join(c.DataDir, "warehouse.duckdb") }

// ParquetDir is where the ETL exports fact tables as partitioned Parquet files.
func (c Config) ParquetDir() string { return filepath.Join(c.DataDir, "parquet") }

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
