//go:build unix

package warehouse

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive, non-blocking lock so only one ETL builds at a
// time (for example `duckstore etl` in a terminal and the UI button).
func lockFile(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
