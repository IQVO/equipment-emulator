package asrs

import (
	"errors"
	"testing"
	"time"
)

func newSys(t *testing.T, doubleDeep bool) *System {
	t.Helper()
	s, err := New("ASRS1", Layout{Aisles: 2, Levels: 5, Slots: 20, DoubleDeep: doubleDeep, Ports: []Port{
		{ID: "IN1", Aisle: 1, Kind: PortInput}, {ID: "OUT1", Aisle: 1, Kind: PortOutput},
	}}, Timing{Base: 2 * time.Second, PerLevel: time.Second, PerSlot: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func pos(level, slot, depth int) Position {
	return Position{Aisle: 1, Level: level, Slot: slot, Depth: depth}
}

func kinds(evs []Event) []EventKind {
	out := []EventKind{}
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func reason(t *testing.T, err error) string {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %v", err)
	}
	return re.Reason
}

func TestStoreFromInputPort(t *testing.T) {
	s := newSys(t, false)
	if err := s.PlaceAtPort("IN1", "C1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Submit(Job{ID: "j1", Kind: KindStore, ContainerID: "C1", Port: "IN1", To: pos(2, 10, 1)}); err != nil {
		t.Fatal(err)
	}
	evs := s.Tick(time.Second) // starts; needs 2+2+1 = 5 s
	if len(evs) != 1 || evs[0].Kind != EventUnitState || evs[0].State != UnitExecute || evs[0].UnitID != "A1" {
		t.Fatalf("start events = %+v", evs)
	}
	evs = s.Tick(4 * time.Second)
	if got := kinds(evs); len(got) != 2 || got[0] != EventJobDone || got[1] != EventUnitState {
		t.Fatalf("done events = %+v", evs)
	}
	v := s.Snapshot()
	if v.Slots["A1-L2-S10-D1"] != "C1" || v.Ports["IN1"] != "" {
		t.Fatalf("view = %+v", v)
	}
}

func TestRetrievePresentsAtOutputPort(t *testing.T) {
	s := newSys(t, false)
	_ = s.Seed(pos(1, 1, 1), "C2")
	_ = s.Submit(Job{ID: "j2", Kind: KindRetrieve, ContainerID: "C2", From: pos(1, 1, 1), Port: "OUT1"})
	evs := s.Tick(10 * time.Second)
	var arrival *Event
	for i := range evs {
		if evs[i].Kind == EventPortArrival {
			arrival = &evs[i]
		}
	}
	if arrival == nil || arrival.PortID != "OUT1" || arrival.ContainerID != "C2" {
		t.Fatalf("events = %+v", evs)
	}
	if err := s.TakeFromPort("OUT1", "C2"); err != nil {
		t.Fatal(err)
	}
	if v := s.Snapshot(); v.Ports["OUT1"] != "" || v.Slots["A1-L1-S1-D1"] != "" {
		t.Fatalf("view = %+v", v)
	}
}

func TestNoReadReportsArrivalWithoutContainer(t *testing.T) {
	s := newSys(t, false)
	_ = s.Seed(pos(1, 1, 1), "C3")
	_ = s.SetNoRead("OUT1", true)
	_ = s.Submit(Job{ID: "j3", Kind: KindRetrieve, ContainerID: "C3", From: pos(1, 1, 1), Port: "OUT1"})
	for _, e := range s.Tick(10 * time.Second) {
		if e.Kind == EventPortArrival && e.ContainerID != "" {
			t.Fatalf("no-read should hide the container: %+v", e)
		}
	}
	if v := s.Snapshot(); v.Ports["OUT1"] != "C3" {
		t.Fatalf("the container is physically at the port: %+v", v)
	}
}

func TestRuntimeFailures(t *testing.T) {
	s := newSys(t, true)
	_ = s.Seed(pos(1, 1, 1), "FRONT")
	_ = s.Seed(pos(1, 1, 2), "BACK")
	_ = s.Seed(pos(2, 1, 1), "OCC")
	_ = s.PlaceAtPort("IN1", "C4")
	_ = s.Submit(Job{ID: "blocked", Kind: KindRetrieve, ContainerID: "BACK", From: pos(1, 1, 2), Port: "OUT1"})
	_ = s.Submit(Job{ID: "occupied", Kind: KindStore, ContainerID: "C4", Port: "IN1", To: pos(2, 1, 1)})
	_ = s.Submit(Job{ID: "missing", Kind: KindRetrieve, ContainerID: "NOPE", From: pos(3, 1, 1), Port: "OUT1"})
	failed := map[string]string{}
	for _, e := range s.Tick(time.Second) {
		if e.Kind == EventJobFailed {
			failed[e.JobID] = e.Reason
		}
	}
	want := map[string]string{"blocked": ReasonPositionBlocked, "occupied": ReasonPositionOccupied, "missing": ReasonContainerNotAtPosition}
	for id, r := range want {
		if failed[id] != r {
			t.Errorf("job %s failed with %q, want %q", id, failed[id], r)
		}
	}
}

func TestSubmitRejectsStaticErrors(t *testing.T) {
	s := newSys(t, false)
	cases := []struct {
		job  Job
		want string
	}{
		{Job{ID: "a", Kind: KindStore, ContainerID: "C", Port: "NOPE", To: pos(1, 1, 1)}, ReasonUnknownPort},
		{Job{ID: "b", Kind: KindStore, ContainerID: "C", Port: "OUT1", To: pos(1, 1, 1)}, ReasonWrongPortKind},
		{Job{ID: "c", Kind: KindStore, ContainerID: "C", Port: "IN1", To: pos(9, 1, 1)}, ReasonInvalidPosition},
		{Job{ID: "d", Kind: KindStore, ContainerID: "C", Port: "IN1", To: pos(1, 1, 2)}, ReasonInvalidPosition},
		{Job{ID: "e", Kind: KindStore, ContainerID: "C", Port: "IN1", To: Position{Aisle: 2, Level: 1, Slot: 1, Depth: 1}}, ReasonAisleMismatch},
	}
	for _, c := range cases {
		if got := reason(t, s.Submit(c.job)); got != c.want {
			t.Errorf("job %s: %s, want %s", c.job.ID, got, c.want)
		}
	}
	ok := Job{ID: "f", Kind: KindMove, ContainerID: "C", From: pos(1, 1, 1), To: pos(1, 2, 1)}
	if err := s.Submit(ok); err != nil {
		t.Fatal(err)
	}
	if got := reason(t, s.Submit(ok)); got != ReasonDuplicateJob {
		t.Fatalf("duplicate: %s", got)
	}
}

func TestUnitFaultHoldsTheJob(t *testing.T) {
	s := newSys(t, false)
	_ = s.Seed(pos(1, 1, 1), "C5")
	_ = s.Submit(Job{ID: "j5", Kind: KindRetrieve, ContainerID: "C5", From: pos(1, 1, 1), Port: "OUT1"})
	s.Tick(time.Second)
	_ = s.SetUnitFault(1, true)
	evs := s.Tick(time.Minute)
	if len(evs) != 1 || evs[0].Kind != EventUnitState || evs[0].State != UnitHeld {
		t.Fatalf("fault events = %+v", evs)
	}
	_ = s.SetUnitFault(1, false)
	done := false
	for _, e := range s.Tick(10 * time.Second) {
		if e.Kind == EventJobDone && e.JobID == "j5" {
			done = true
		}
	}
	if !done {
		t.Fatal("job should finish after the fault clears")
	}
}

func TestCancelAndFifo(t *testing.T) {
	s := newSys(t, false)
	_ = s.Seed(pos(1, 1, 1), "A")
	_ = s.Seed(pos(1, 2, 1), "B")
	_ = s.Submit(Job{ID: "first", Kind: KindMove, ContainerID: "A", From: pos(1, 1, 1), To: pos(1, 3, 1)})
	_ = s.Submit(Job{ID: "second", Kind: KindMove, ContainerID: "B", From: pos(1, 2, 1), To: pos(1, 4, 1)})
	s.Tick(time.Millisecond)
	if got := reason(t, s.Cancel("first")); got != ReasonJobRunning {
		t.Fatalf("cancel running: %s", got)
	}
	if err := s.Cancel("second"); err != nil {
		t.Fatal(err)
	}
	if got := reason(t, s.Cancel("nope")); got != ReasonUnknownJob {
		t.Fatalf("cancel unknown: %s", got)
	}
}

func TestPortSeedAndFaultGuards(t *testing.T) {
	s := newSys(t, true)
	if err := s.PlaceAtPort("IN1", "C1"); err != nil {
		t.Fatal(err)
	}
	if got := reason(t, s.PlaceAtPort("IN1", "C2")); got != ReasonPortOccupied {
		t.Fatalf("occupied: %s", got)
	}
	if got := reason(t, s.PlaceAtPort("OUT1", "C2")); got != ReasonWrongPortKind {
		t.Fatalf("kind: %s", got)
	}
	if got := reason(t, s.PlaceAtPort("NOPE", "C2")); got != ReasonUnknownPort {
		t.Fatalf("unknown: %s", got)
	}
	if got := reason(t, s.TakeFromPort("OUT1", "C1")); got != ReasonPortEmpty {
		t.Fatalf("empty: %s", got)
	}
	if got := reason(t, s.TakeFromPort("IN1", "C1")); got != ReasonWrongPortKind {
		t.Fatalf("take kind: %s", got)
	}
	if err := s.Seed(pos(1, 1, 1), "C3"); err != nil {
		t.Fatal(err)
	}
	if got := reason(t, s.Seed(pos(1, 1, 1), "C4")); got != ReasonPositionOccupied {
		t.Fatalf("seed occupied: %s", got)
	}
	if got := reason(t, s.Seed(pos(99, 1, 1), "C4")); got != ReasonInvalidPosition {
		t.Fatalf("seed invalid: %s", got)
	}
	if got := reason(t, s.SetUnitFault(9, true)); got != ReasonInvalidPosition {
		t.Fatalf("fault: %s", got)
	}
	if got := reason(t, s.SetNoRead("NOPE", true)); got != ReasonUnknownPort {
		t.Fatalf("noread: %s", got)
	}
	if err := s.SetNoRead("OUT1", true); err != nil {
		t.Fatal(err)
	}
}

func TestUnitStatesListsEveryAisle(t *testing.T) {
	s := newSys(t, false)
	evs := s.UnitStates()
	if len(evs) != 2 {
		t.Fatalf("want one event per aisle, got %d", len(evs))
	}
	for _, e := range evs {
		if e.Kind != EventUnitState {
			t.Fatalf("kind %v", e.Kind)
		}
	}
}

func TestSetNoReadRejectsAnInputPort(t *testing.T) {
	s := newSys(t, false)
	if got := reason(t, s.SetNoRead("IN1", true)); got != ReasonWrongPortKind {
		t.Fatalf("no-read on an input port: %s", got)
	}
}
