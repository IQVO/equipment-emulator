package robot

import (
	"testing"
	"time"
)

func TestEmptyBatteryHaltsTheRobotEvenWithoutDrain(t *testing.T) {
	c := cfg()
	c.DrainPerMeter = 0 // driving would not lower the charge, so only the empty-battery stop holds the robot
	r := New(c)
	_ = r.AcceptOrder(straight())
	r.SetStateOfCharge(0)
	r.Tick(5*time.Second, &fakePeripherals{})
	if s := r.Snapshot(); s.Position.X != 0 || s.Driving || s.LastNodeID != "n1" {
		t.Fatalf("state = %+v", s)
	}
}

func TestTickEndingExactlyOnANodeReportsTheRobotStanding(t *testing.T) {
	r := New(cfg())
	_ = r.AcceptOrder(path(nil, Position{}, Position{X: 2}, Position{X: 12}))
	r.Tick(2*time.Second, &fakePeripherals{}) // exactly the 2 m of e1 at 1 m/s
	s := r.Snapshot()
	if s.LastNodeID != "n2" || s.Position.X != 2 || s.Driving || len(s.NodeStates) != 1 {
		t.Fatalf("state = %+v", s)
	}
}

func TestStepThatExactlyReachesTheTargetSnapsToItsPose(t *testing.T) {
	r := New(cfg())
	r.pos = Position{X: 0, Y: 0, Theta: 0, MapID: "S0"}
	to := Position{X: 3, Y: 4, Theta: 1.5, MapID: "S1"}
	r.moveToward(to, 5) // dist is exactly 5
	if r.pos != to {
		t.Fatalf("pos = %+v, want %+v", r.pos, to)
	}
}

func TestEdgeMaxSpeedEqualToRobotSpeed(t *testing.T) {
	r := New(cfg()) // 1 m/s
	_ = r.AcceptOrder(path([]float64{1}, Position{}, Position{X: 10}))
	r.Tick(3*time.Second, &fakePeripherals{})
	if x := r.Snapshot().Position.X; !near(x, 3) {
		t.Fatalf("x = %v, want 3", x)
	}
}
