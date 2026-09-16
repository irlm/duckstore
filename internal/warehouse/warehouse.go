package warehouse

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/duckdb/duckdb-go/v2"

	"github.com/irlm/duckstore/internal/config"
)

var ErrNotBuilt = errors.New("the warehouse has not been built yet: run `make etl` or use the Run ETL button")

// Warehouse gives the analytics side read access to the latest warehouse file.
//
// How it is opened (sharing pattern: "one process owns the file"):
//
//	in-memory DuckDB
//	  ├── ATTACH 'data/warehouse.duckdb' AS wh (READ_ONLY)   <- the star schema
//	  ├── USE wh                                              <- so "dw.fact_sales" works
//	  └── ATTACH 'postgres://…' AS pg (TYPE postgres, READ_ONLY) <- live store data
//
// The file is attached READ_ONLY, so the ETL can build the next version in a
// separate file at the same time. When the ETL swaps the file, Reload opens
// the new one and closes the old one after running queries finish.
type Warehouse struct {
	cfg config.Config

	mu     sync.RWMutex
	db     *sql.DB
	stat   os.FileInfo
	pgErr  error
	opened time.Time
}

func New(cfg config.Config) *Warehouse { return &Warehouse{cfg: cfg} }

// With runs fn with the current database while holding a read lock, so a
// reload waits until fn returns.
func (w *Warehouse) With(fn func(db *sql.DB) error) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.db == nil {
		return ErrNotBuilt
	}
	return fn(w.db)
}

// PostgresAttachError reports why the live Postgres attach failed, if it did.
func (w *Warehouse) PostgresAttachError() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.pgErr
}

// Reload opens the warehouse file if it changed (or was never opened).
func (w *Warehouse) Reload(ctx context.Context) error {
	path := w.cfg.WarehousePath()
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	w.mu.RLock()
	same := w.stat != nil && os.SameFile(fi, w.stat) && fi.ModTime().Equal(w.stat.ModTime())
	w.mu.RUnlock()
	if same {
		return nil
	}

	db, pgErr, err := openWarehouse(ctx, path, w.cfg.PostgresURL)
	if err != nil {
		return err
	}
	w.mu.Lock()
	old := w.db
	w.db, w.stat, w.pgErr, w.opened = db, fi, pgErr, time.Now()
	w.mu.Unlock()
	if old != nil {
		old.Close()
	}
	log.Printf("warehouse: opened %s (%.0f MB)", path, float64(fi.Size())/1e6)
	return nil
}

// Watch checks for a new warehouse file every interval (for example one
// built by `duckstore etl` in another terminal).
func (w *Warehouse) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.Reload(ctx); err != nil {
				log.Printf("warehouse: reload failed: %v", err)
			}
		}
	}
}

func (w *Warehouse) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.db == nil {
		return nil
	}
	err := w.db.Close()
	w.db = nil
	return err
}

func openWarehouse(ctx context.Context, path, pgURL string) (db *sql.DB, pgErr, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	// The init function runs for every new connection in the pool. ATTACH is
	// shared by all connections of the database; USE and SET are per connection.
	connector, err := duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		for _, q := range []string{
			"SET TimeZone = 'UTC'",
			fmt.Sprintf("ATTACH IF NOT EXISTS %s AS wh (READ_ONLY)", quote(abs)),
			"USE wh",
		} {
			if _, err := execer.ExecContext(context.Background(), q, nil); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	db = sql.OpenDB(connector)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, nil, err
	}
	// Live Postgres access is optional: analytics still work without it.
	for _, q := range []string{
		"INSTALL postgres",
		"LOAD postgres",
		fmt.Sprintf("ATTACH IF NOT EXISTS %s AS pg (TYPE postgres, READ_ONLY)", quote(pgURL)),
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			pgErr = err
			break
		}
	}
	return db, pgErr, nil
}
