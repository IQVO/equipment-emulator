// The repo-specific half of the idempotency replay-class test (warehouse-harness-template
// templates/idempotency/replay_classes_wiring_test.go.tmpl). The emulator's store sits behind the
// middleware in process memory (ADR 0006): there is no database and no unit of work, so Write is a
// plain counted write (engine state changes are immediate) and KeyRows asks the store itself.
package http_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	ahttp "github.com/IQVO/equipment-emulator/internal/adapters/inbound/http"
)

// newReplayOps returns the six operations over ONE clean in-memory store per call.
func newReplayOps(t *testing.T) replayOps {
	t.Helper()
	idem := ahttp.NewIdempotency(time.Now)
	var mu sync.Mutex
	writes := map[string]int{}
	return replayOps{
		Catalogue: func() []problemEntry {
			var out []problemEntry
			for _, e := range ahttp.Catalogue() {
				out = append(out, problemEntry{Slug: e.Slug, Status: e.Status, Class: e.Class})
			}
			return out
		},
		Middleware: func(_ *testing.T, next http.Handler) http.Handler { return idem.Middleware(next) },
		Refuse:     func(w http.ResponseWriter, r *http.Request, e problemEntry) { ahttp.Refuse(w, r, e.Slug) },
		Write: func(_ context.Context, tag string) error {
			mu.Lock()
			defer mu.Unlock()
			writes[tag]++
			return nil
		},
		Committed: func(_ *testing.T, tag string) int {
			mu.Lock()
			defer mu.Unlock()
			return writes[tag]
		},
		KeyRows: func(_ *testing.T, key string) int { return idem.KeyRows(key) },
	}
}
