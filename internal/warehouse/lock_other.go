//go:build !unix

package warehouse

// lockFile is a no-op outside Unix; DuckDB's own file lock still prevents two
// writers on the same .building file.
func lockFile(string) (func(), error) { return func() {}, nil }
