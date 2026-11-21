package http

import (
	"net/http"
	"sync"
	"time"
)

// storedAnswer is one stored response under a key.
type storedAnswer struct {
	fingerprint string
	status      int
	header      http.Header
	body        []byte
	at          time.Time
}

// keyStore is the in-memory idempotency store (ADR 0006). It lives and dies with the process:
// a restart forgets every key, and two replicas would not share one, which is why the chart runs
// exactly one. Each save purges every answer older than the window, so memory is bounded by one
// window of admin requests.
type keyStore struct {
	mu   sync.Mutex
	rows map[string]storedAnswer
	busy map[string]chan struct{}
}

func newKeyStore() *keyStore {
	return &keyStore{rows: map[string]storedAnswer{}, busy: map[string]chan struct{}{}}
}

// acquire waits until no other request holds key, takes it and returns its release.
func (s *keyStore) acquire(key string) (release func()) {
	s.mu.Lock()
	for {
		held, ok := s.busy[key]
		if !ok {
			break
		}
		s.mu.Unlock()
		<-held
		s.mu.Lock()
	}
	done := make(chan struct{})
	s.busy[key] = done
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.busy, key)
		s.mu.Unlock()
		close(done)
	}
}

// lookup returns the answer stored under key, unless it is older than notBefore (then it is dropped).
func (s *keyStore) lookup(key string, notBefore time.Time) (storedAnswer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.rows[key]
	if ok && rec.at.Before(notBefore) {
		delete(s.rows, key)
		return storedAnswer{}, false
	}
	return rec, ok
}

// save stores rec under key and purges every answer older than notBefore.
func (s *keyStore) save(key string, rec storedAnswer, notBefore time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, r := range s.rows {
		if r.at.Before(notBefore) {
			delete(s.rows, k)
		}
	}
	s.rows[key] = rec
}

// count reports how many answers are stored under key (0 or 1).
func (s *keyStore) count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[key]; ok {
		return 1
	}
	return 0
}
