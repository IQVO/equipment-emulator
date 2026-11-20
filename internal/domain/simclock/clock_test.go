package simclock

import (
	"testing"
	"time"
)

func TestAdvanceMovesTimeForwardOnly(t *testing.T) {
	start := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	c := NewSim(start)
	if got := c.Advance(1500 * time.Millisecond); !got.Equal(start.Add(1500 * time.Millisecond)) {
		t.Fatalf("Advance = %v", got)
	}
	c.Advance(-time.Second)
	c.Advance(0)
	if got := c.Now(); !got.Equal(start.Add(1500 * time.Millisecond)) {
		t.Fatalf("non-positive advance moved time: %v", got)
	}
}

func TestScaleAppliesSpeed(t *testing.T) {
	cases := []struct {
		real  time.Duration
		speed float64
		want  time.Duration
	}{
		{100 * time.Millisecond, 1, 100 * time.Millisecond},
		{100 * time.Millisecond, 10, time.Second},
		{100 * time.Millisecond, 0, 0},
		{100 * time.Millisecond, -2, 0},
	}
	for _, c := range cases {
		if got := Scale(c.real, c.speed); got != c.want {
			t.Errorf("Scale(%v, %v) = %v, want %v", c.real, c.speed, got, c.want)
		}
	}
}
