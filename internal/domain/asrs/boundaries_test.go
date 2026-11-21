package asrs

import (
	"testing"
	"time"
)

func TestNewAcceptsTheSmallestLayoutAndRejectsAZeroDimension(t *testing.T) {
	s, err := New("MIN", Layout{Aisles: 1, Levels: 1, Slots: 1}, Timing{})
	if err != nil {
		t.Fatalf("a 1x1x1 rack is valid: %v", err)
	}
	if err := s.Seed(Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}, "C1"); err != nil {
		t.Fatalf("the only position of a 1x1x1 rack: %v", err)
	}
	for name, l := range map[string]Layout{
		"no aisles": {Aisles: 0, Levels: 1, Slots: 1},
		"no levels": {Aisles: 1, Levels: 0, Slots: 1},
		"no slots":  {Aisles: 1, Levels: 1, Slots: 0},
	} {
		if _, err := New("BAD", l, Timing{}); err == nil || err.Error() != "asrs BAD: aisles, levels and slots must be positive" {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestNewAcceptsAPortOnTheFirstAndLastAisleOnly(t *testing.T) {
	ports := func(aisle int) Layout {
		return Layout{Aisles: 2, Levels: 1, Slots: 1, Ports: []Port{{ID: "P", Aisle: aisle, Kind: PortInput}}}
	}
	for _, aisle := range []int{1, 2} {
		if _, err := New("OK", ports(aisle), Timing{}); err != nil {
			t.Errorf("port on aisle %d of 2: %v", aisle, err)
		}
	}
	for _, aisle := range []int{0, 3} {
		if _, err := New("BAD", ports(aisle), Timing{}); err == nil || err.Error() != "asrs BAD: port P is outside the aisles" {
			t.Errorf("port on aisle %d of 2: err = %v", aisle, err)
		}
	}
}

func TestPositionsReachTheLastLevelAndSlot(t *testing.T) {
	s := newSys(t, false) // 5 levels, 20 slots
	if err := s.Seed(pos(5, 20, 1), "TOP"); err != nil {
		t.Fatalf("level 5 slot 20 is the last valid position: %v", err)
	}
	for _, p := range []Position{pos(6, 20, 1), pos(5, 21, 1)} {
		if got := reason(t, s.Seed(p, "OUT")); got != ReasonInvalidPosition {
			t.Errorf("%s: %s", p, got)
		}
	}
}

// assertJobDuration submits j and measures, to the nanosecond, how long the unit
// runs it: one nanosecond short of want the job is still running, at want
// it is done.
func assertJobDuration(t *testing.T, s *System, j Job, want time.Duration) {
	t.Helper()
	if err := s.Submit(j); err != nil {
		t.Fatal(err)
	}
	done := func(evs []Event) bool {
		for _, e := range evs {
			if e.Kind == EventJobFailed {
				t.Fatalf("job %s failed: %s", j.ID, e.Reason)
			}
			if e.Kind == EventJobDone && e.JobID == j.ID {
				return true
			}
		}
		return false
	}
	if done(s.Tick(want - time.Nanosecond)) {
		t.Fatalf("job %s finished before %v", j.ID, want)
	}
	if !done(s.Tick(time.Nanosecond)) {
		t.Fatalf("job %s not finished at %v", j.ID, want)
	}
}

// Timing is Base 2 s + 1 s per level + 100 ms per slot of the rack position
// the job works at.
func TestJobDurationUsesTheRackPositionOfTheJob(t *testing.T) {
	t.Run("store: the target position", func(t *testing.T) {
		s := newSys(t, false)
		_ = s.PlaceAtPort("IN1", "C1")
		assertJobDuration(t, s, Job{ID: "s", Kind: KindStore, ContainerID: "C1", Port: "IN1", To: pos(3, 7, 1)},
			2*time.Second+3*time.Second+700*time.Millisecond)
	})
	t.Run("retrieve: the source position", func(t *testing.T) {
		s := newSys(t, false)
		_ = s.Seed(pos(4, 9, 1), "C2")
		assertJobDuration(t, s, Job{ID: "r", Kind: KindRetrieve, ContainerID: "C2", From: pos(4, 9, 1), Port: "OUT1"},
			2*time.Second+4*time.Second+900*time.Millisecond)
	})
	t.Run("move down: the higher source position", func(t *testing.T) {
		s := newSys(t, false)
		_ = s.Seed(pos(4, 3, 1), "C3")
		assertJobDuration(t, s, Job{ID: "md", Kind: KindMove, ContainerID: "C3", From: pos(4, 3, 1), To: pos(2, 9, 1)},
			2*time.Second+4*time.Second+300*time.Millisecond)
	})
	t.Run("move up: the higher target position", func(t *testing.T) {
		s := newSys(t, false)
		_ = s.Seed(pos(2, 3, 1), "C4")
		assertJobDuration(t, s, Job{ID: "mu", Kind: KindMove, ContainerID: "C4", From: pos(2, 3, 1), To: pos(4, 9, 1)},
			2*time.Second+4*time.Second+900*time.Millisecond)
	})
	t.Run("move along a level: the target position", func(t *testing.T) {
		s := newSys(t, false)
		_ = s.Seed(pos(3, 3, 1), "C5")
		assertJobDuration(t, s, Job{ID: "ml", Kind: KindMove, ContainerID: "C5", From: pos(3, 3, 1), To: pos(3, 9, 1)},
			2*time.Second+3*time.Second+900*time.Millisecond)
	})
}

func TestSnapshotListsUnitsInIDOrder(t *testing.T) {
	s := newSys(t, false)
	_ = s.Seed(pos(1, 1, 1), "C1")
	_ = s.Submit(Job{ID: "j1", Kind: KindMove, ContainerID: "C1", From: pos(1, 1, 1), To: pos(1, 2, 1)})
	_ = s.Submit(Job{ID: "j2", Kind: KindMove, ContainerID: "C1", From: pos(1, 2, 1), To: pos(1, 3, 1)})
	s.Tick(time.Second)
	units := s.Snapshot().Units
	want := []UnitView{
		{ID: "A1", State: UnitExecute, CurrentJob: "j1", Queued: 1},
		{ID: "A2", State: UnitIdle, CurrentJob: "", Queued: 0},
	}
	if len(units) != len(want) {
		t.Fatalf("units = %+v", units)
	}
	for i := range want {
		if units[i] != want[i] {
			t.Errorf("units[%d] = %+v, want %+v", i, units[i], want[i])
		}
	}
}
