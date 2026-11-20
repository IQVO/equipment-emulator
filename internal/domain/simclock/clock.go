// Package simclock is the emulator's simulated time. Only the engine advances it.
package simclock

import (
	"sync"
	"time"
)

// Sim is a monotonic simulated clock.
type Sim struct {
	mu  sync.Mutex
	now time.Time
}

// NewSim starts simulated time at start (normalised to UTC).
func NewSim(start time.Time) *Sim { return &Sim{now: start.UTC()} }

// Now returns the current simulated time.
func (s *Sim) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Advance moves simulated time forward by d and returns the new time.
// A non-positive d is ignored: simulated time never goes backwards.
func (s *Sim) Advance(d time.Duration) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d > 0 {
		s.now = s.now.Add(d)
	}
	return s.now
}

// Scale converts a real-time interval into simulated time at the given
// speed factor. A non-positive speed freezes the simulation.
func Scale(real time.Duration, speed float64) time.Duration {
	if speed <= 0 {
		return 0
	}
	return time.Duration(float64(real) * speed)
}
