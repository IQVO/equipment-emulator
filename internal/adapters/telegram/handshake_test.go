package telegram

import (
	"testing"
	"time"
)

func TestDedupRemembersAndEvictsOldest(t *testing.T) {
	d := NewDedup(2)
	d.Remember("a", Telegram{ID: "ack-a"})
	d.Remember("b", Telegram{ID: "ack-b"})
	if ack, ok := d.Lookup("a"); !ok || ack.ID != "ack-a" {
		t.Fatal("a should be remembered")
	}
	d.Remember("c", Telegram{ID: "ack-c"})
	if _, ok := d.Lookup("a"); ok {
		t.Fatal("a should be evicted")
	}
}

func TestRetransmitter(t *testing.T) {
	now := time.Unix(1000, 0)
	r := NewRetransmitter(2*time.Second, 3)
	r.Track(Telegram{ID: "t1"}, now)
	r.Track(Telegram{ID: "t2"}, now)
	if resend, _ := r.Due(now.Add(time.Second)); len(resend) != 0 {
		t.Fatal("nothing due before the interval")
	}
	if !r.Ack("t2") || r.Ack("t2") {
		t.Fatal("ack should clear exactly once")
	}
	var sends int
	var expired []Telegram
	for i := 1; i <= 4; i++ {
		resend, exp := r.Due(now.Add(time.Duration(2*i) * time.Second))
		sends += len(resend)
		expired = append(expired, exp...)
	}
	if sends != 3 || len(expired) != 1 || expired[0].ID != "t1" {
		t.Fatalf("sends=%d expired=%v", sends, expired)
	}
}
