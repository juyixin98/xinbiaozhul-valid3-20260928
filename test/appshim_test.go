package testutil_test

import (
	"testing"

	"http11subset/internal/app"
	"http11subset/internal/httpx"
	"http11subset/internal/storage"
)

// newAppHandler wires the real business handler for integration tests.
func newAppHandler(t *testing.T, store *storage.Store) httpx.Handler {
	t.Helper()
	return app.New(store)
}
