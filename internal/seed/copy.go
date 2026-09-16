package seed

import (
	"context"
	"math"
	"math/big"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Rows are loaded with the Postgres COPY protocol (pgx CopyFrom), the Postgres
// version of SQL Server's bulk copy (bcp / SqlBulkCopy). It is 10-50x faster
// than INSERT statements.

const batchSize = 2000

// stream is one table being loaded by COPY while a generator produces rows.
// Rows are sent in batches over a channel so the generator and the COPY
// connection run in parallel.
type stream struct {
	table string
	cols  []string
	ch    chan [][]any
	buf   [][]any
	rows  atomic.Int64
}

func newStream(table string, cols ...string) *stream {
	return &stream{table: table, cols: cols, ch: make(chan [][]any, 32), buf: make([][]any, 0, batchSize)}
}

func (s *stream) add(ctx context.Context, row ...any) error {
	s.buf = append(s.buf, row)
	if len(s.buf) < batchSize {
		return nil
	}
	return s.flush(ctx)
}

func (s *stream) flush(ctx context.Context) error {
	if len(s.buf) == 0 {
		return nil
	}
	select {
	case s.ch <- s.buf:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.buf = make([][]any, 0, batchSize)
	return nil
}

// finish flushes what is left and tells the COPY side there are no more rows.
func (s *stream) finish(ctx context.Context) error {
	err := s.flush(ctx)
	close(s.ch)
	return err
}

// copy runs the COPY until the generator calls finish.
func (s *stream) copy(ctx context.Context, pool *pgxpool.Pool) error {
	var batch [][]any
	i := 0
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"store", s.table}, s.cols, pgx.CopyFromFunc(func() ([]any, error) {
		for i >= len(batch) {
			b, ok := <-s.ch
			if !ok {
				return nil, nil
			}
			batch, i = b, 0
		}
		i++
		s.rows.Add(1)
		return batch[i-1], nil
	}))
	return err
}

// copyIter loads a table from an iterator that returns nil when done.
func copyIter(ctx context.Context, pool *pgxpool.Pool, table string, cols []string, next func() []any) (int64, error) {
	return pool.CopyFrom(ctx, pgx.Identifier{"store", table}, cols, pgx.CopyFromFunc(func() ([]any, error) {
		return next(), nil
	}))
}

// copyN loads n rows produced by row(i).
func copyN(ctx context.Context, pool *pgxpool.Pool, table string, cols []string, n int, row func(i int) []any) (int64, error) {
	i := 0
	return copyIter(ctx, pool, table, cols, func() []any {
		if i >= n {
			return nil
		}
		i++
		return row(i - 1)
	})
}

// money converts cents to an exact numeric(…,2) value (no float rounding).
func money(cents int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(cents), Exp: -2, Valid: true}
}

// decimal converts a float to an exact numeric with the given decimals.
func decimal(f float64, decimals int) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(int64(math.Round(f * math.Pow10(decimals)))), Exp: int32(-decimals), Valid: true}
}

// nullID turns 0 into SQL NULL.
func nullID[T int32 | int64](id T) any {
	if id == 0 {
		return nil
	}
	return int64(id)
}
