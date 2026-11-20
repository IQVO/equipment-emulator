package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	IdempotencyKeyHeader      = "Idempotency-Key"
	IdempotencyReplayedHeader = "Idempotency-Replayed"
	// IdempotencyWindow is how long an answer is replayed (fleet rule).
	IdempotencyWindow = 24 * time.Hour
	maxKeyLen         = 128
	// maxBodyBytes bounds a keyed request body; every admin body is a few hundred bytes.
	maxBodyBytes = 64 << 10
)

// Idempotency is the fleet Idempotency-Key middleware with replay classes (ADR 0006), shaped
// after asrs-control's, over an in-memory store: the emulator has no database (ADR 0001), so
// there is no unit of work to join and answers do not survive a restart.
//
// The key is OPTIONAL here: a request without one runs unkeyed. With a key, the handler's answer
// is captured; a 2xx and a FINAL refusal are stored, a RETRYABLE refusal (marked by
// writeProblem), a 5xx and a panic are not, so the key is released. A retry with the same method,
// path and body replays the stored answer with Idempotency-Replayed: true; a different one is 409
// idempotency-key-reused. Concurrent requests under one key run one at a time: the second waits,
// then replays.
type Idempotency struct {
	store *keyStore
	now   func() time.Time
}

func NewIdempotency(now func() time.Time) *Idempotency {
	return &Idempotency{store: newKeyStore(), now: now}
}

func (m *Idempotency) notBefore() time.Time { return m.now().Add(-IdempotencyWindow) }

func (m *Idempotency) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys := r.Header.Values(IdempotencyKeyHeader)
		if len(keys) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if len(keys) != 1 || !validKey(keys[0]) {
			writeProblem(w, r, idempotencyKeyInvalid, "send one Idempotency-Key of 1 to 128 printable ASCII characters, no leading or trailing space")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		_ = r.Body.Close()
		if err != nil || len(body) > maxBodyBytes {
			writeProblem(w, r, badRequest, "the request body could not be read or is larger than 64 KiB")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		m.serve(w, r, next, keys[0], fingerprint(r, body))
	})
}

// serve holds the key for the whole request, so a concurrent duplicate waits and then replays.
func (m *Idempotency) serve(w http.ResponseWriter, r *http.Request, next http.Handler, key, fp string) {
	release := m.store.acquire(key)
	defer release()
	if rec, found := m.store.lookup(key, m.notBefore()); found {
		replay(w, r, rec, fp)
		return
	}
	out := &capture{header: http.Header{}}
	next.ServeHTTP(out, r) // a panic propagates: nothing is stored and release still runs
	if out.code == 0 {
		out.code = http.StatusOK
	}
	if out.code < http.StatusInternalServerError && !out.notStored {
		m.store.save(key, storedAnswer{fingerprint: fp, status: out.code, header: out.header.Clone(),
			body: bytes.Clone(out.body.Bytes()), at: m.now()}, m.notBefore())
	}
	flush(w, out)
}

// validKey is the spec's pattern ^[!-~]([ -~]*[!-~])?$ with length 1-128.
func validKey(k string) bool {
	if k == "" || len(k) > maxKeyLen || k[0] == ' ' || k[len(k)-1] == ' ' {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < ' ' || k[i] > '~' {
			return false
		}
	}
	return true
}

func fingerprint(r *http.Request, body []byte) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n", r.Method, r.URL.EscapedPath())
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// capture records the handler's answer before anything reaches the client.
type capture struct {
	header    http.Header
	code      int
	body      bytes.Buffer
	notStored bool
}

func (c *capture) Header() http.Header { return c.header }
func (c *capture) doNotStore()         { c.notStored = true }

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}

func replay(w http.ResponseWriter, r *http.Request, rec storedAnswer, fp string) {
	if rec.fingerprint != fp {
		writeProblem(w, r, idempotencyKeyReused, "the Idempotency-Key was used with a different request (method, path or body)")
		return
	}
	copyHeader(w.Header(), rec.header)
	w.Header().Set(IdempotencyReplayedHeader, "true")
	w.WriteHeader(rec.status)
	_, _ = w.Write(rec.body)
}

func flush(w http.ResponseWriter, out *capture) {
	copyHeader(w.Header(), out.header)
	w.WriteHeader(out.code)
	_, _ = w.Write(out.body.Bytes())
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
