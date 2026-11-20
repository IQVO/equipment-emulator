package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// counting answers 204 and counts its calls.
type counting struct{ calls atomic.Int32 }

func (c *counting) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	c.calls.Add(1)
	w.WriteHeader(http.StatusNoContent)
}

func keyed(h http.Handler, key, body string, set bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/faults", strings.NewReader(body))
	if set {
		req.Header.Set(IdempotencyKeyHeader, key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The key is OPTIONAL on the admin API (ADR 0006): asrs-control's e2e test and
// every existing caller send none, and a POST without one simply runs.
func TestWithoutAKeyEveryRequestRuns(t *testing.T) {
	next := &counting{}
	h := NewIdempotency(time.Now).Middleware(next)
	for range 3 {
		if r := keyed(h, "", `{}`, false); r.Code != http.StatusNoContent || r.Header().Get(IdempotencyReplayedHeader) != "" {
			t.Fatalf("unkeyed = %d replayed=%q", r.Code, r.Header().Get(IdempotencyReplayedHeader))
		}
	}
	if next.calls.Load() != 3 {
		t.Fatalf("handler ran %d times, want 3", next.calls.Load())
	}
}

func TestAMalformedKeyIsRefusedBeforeTheHandler(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"129 characters":   strings.Repeat("k", 129),
		"a control char":   "k\x01y",
		"non-ASCII":        "clé",
		"leading space":    " k",
		"trailing space":   "k ",
		"only a space":     " ",
		"a tab in between": "k\ty",
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			next := &counting{}
			r := keyed(NewIdempotency(time.Now).Middleware(next), key, `{}`, true)
			if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), `"code":"idempotency-key-invalid"`) {
				t.Fatalf("key %q = %d %s, want 400 idempotency-key-invalid", key, r.Code, r.Body)
			}
			if next.calls.Load() != 0 {
				t.Fatal("the handler ran for a malformed key")
			}
		})
	}
	for _, key := range []string{"k", strings.Repeat("k", 128), "a b", "550e8400-e29b-41d4-a716-446655440000", "!~"} {
		if r := keyed(NewIdempotency(time.Now).Middleware(&counting{}), key, `{}`, true); r.Code != http.StatusNoContent {
			t.Errorf("valid key %q = %d", key, r.Code)
		}
	}
}

func TestARepeatedKeyHeaderIsRefused(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/faults", strings.NewReader(`{}`))
	req.Header.Add(IdempotencyKeyHeader, "a")
	req.Header.Add(IdempotencyKeyHeader, "b")
	rec := httptest.NewRecorder()
	NewIdempotency(time.Now).Middleware(&counting{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("two Idempotency-Key headers = %d, want 400", rec.Code)
	}
}

func TestAnOversizedBodyIsABadRequest(t *testing.T) {
	r := keyed(NewIdempotency(time.Now).Middleware(&counting{}), "k", strings.Repeat("x", maxBodyBytes+1), true)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), `"code":"bad-request"`) {
		t.Fatalf("oversized body = %d %s", r.Code, r.Body)
	}
}

// Concurrent duplicates under one key: one runs, the others wait on the key and
// replay its answer.
func TestConcurrentDuplicatesRunOnceAndReplay(t *testing.T) {
	gate := make(chan struct{})
	var calls atomic.Int32
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		<-gate
		w.WriteHeader(http.StatusNoContent)
	})
	h := NewIdempotency(time.Now).Middleware(slow)
	const n = 8
	var wg sync.WaitGroup
	replays := atomic.Int32{}
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := keyed(h, "burst", `{"a":1}`, true); r.Header().Get(IdempotencyReplayedHeader) == "true" {
				replays.Add(1)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()
	if calls.Load() != 1 || replays.Load() != n-1 {
		t.Fatalf("handler ran %d times with %d replays, want 1 and %d", calls.Load(), replays.Load(), n-1)
	}
}

// The window is 24 hours: an older answer is neither replayed nor kept.
func TestAnAnswerOlderThanTheWindowIsForgotten(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	idem := NewIdempotency(func() time.Time { return now })
	next := &counting{}
	h := idem.Middleware(next)

	keyed(h, "old", `{}`, true)
	now = now.Add(IdempotencyWindow - time.Second)
	if r := keyed(h, "old", `{}`, true); r.Header().Get(IdempotencyReplayedHeader) != "true" {
		t.Fatal("an answer inside the window was not replayed")
	}
	now = now.Add(2 * time.Second)
	if r := keyed(h, "old", `{}`, true); r.Header().Get(IdempotencyReplayedHeader) != "" || next.calls.Load() != 2 {
		t.Fatalf("an expired answer was replayed (calls %d)", next.calls.Load())
	}

	keyed(h, "stale", `{}`, true)
	now = now.Add(IdempotencyWindow + time.Second)
	keyed(h, "fresh", `{}`, true) // a save purges every expired answer
	if idem.KeyRows("stale") != 0 || idem.KeyRows("fresh") != 1 {
		t.Fatalf("rows stale=%d fresh=%d, want 0 and 1", idem.KeyRows("stale"), idem.KeyRows("fresh"))
	}
}
