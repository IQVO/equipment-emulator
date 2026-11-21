package telegram

import (
	"sync"
	"time"
)

// Dedup remembers the ACK sent for the last `capacity` telegram ids.
type Dedup struct {
	mu    sync.Mutex
	cap   int
	order []string
	acks  map[string]Telegram
}

func NewDedup(capacity int) *Dedup { return &Dedup{cap: capacity, acks: map[string]Telegram{}} }

func (d *Dedup) Lookup(id string) (Telegram, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, ok := d.acks[id]
	return a, ok
}

func (d *Dedup) Remember(id string, ack Telegram) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.acks[id]; ok {
		return
	}
	d.acks[id] = ack
	d.order = append(d.order, id)
	if len(d.order) > d.cap {
		delete(d.acks, d.order[0])
		d.order = d.order[1:]
	}
}

type pending struct {
	t        Telegram
	next     time.Time
	attempts int
}

// Retransmitter tracks sent telegrams until they are acknowledged.
type Retransmitter struct {
	mu          sync.Mutex
	interval    time.Duration
	maxAttempts int
	items       map[string]*pending
}

func NewRetransmitter(interval time.Duration, maxAttempts int) *Retransmitter {
	return &Retransmitter{interval: interval, maxAttempts: maxAttempts, items: map[string]*pending{}}
}

func (r *Retransmitter) Track(t Telegram, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[t.ID] = &pending{t: t, next: now.Add(r.interval)}
}

// Ack clears a tracked telegram; it reports whether one was tracked.
func (r *Retransmitter) Ack(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.items[id]
	delete(r.items, id)
	return ok
}

// Due returns the telegrams to resend now and those that ran out of attempts.
func (r *Retransmitter) Due(now time.Time) (resend, expired []Telegram) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range r.items {
		if now.Before(p.next) {
			continue
		}
		if p.attempts >= r.maxAttempts {
			expired = append(expired, p.t)
			delete(r.items, id)
			continue
		}
		p.attempts++
		p.next = now.Add(r.interval)
		resend = append(resend, p.t)
	}
	return resend, expired
}
