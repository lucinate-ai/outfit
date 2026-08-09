package remote

// must1 unwraps a (value, error) path resolver in tests, failing on error.
// The config-dir resolvers now return an error; a test that only needs the
// path wraps the call in this.
func must1[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
