package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

type recorder struct {
	states      []robot.State
	connections []string
	factsheets  int
	events      []asrs.Event
}

func (r *recorder) PublishState(_ context.Context, s robot.State, _ time.Time) error {
	r.states = append(r.states, s)
	return nil
}
func (r *recorder) PublishConnection(_ context.Context, _, serial, st string, _ time.Time) error {
	r.connections = append(r.connections, serial+":"+st)
	return nil
}
func (r *recorder) PublishFactsheet(context.Context, robot.Factsheet, time.Time) error {
	r.factsheets++
	return nil
}
func (r *recorder) PublishEvents(_ context.Context, _ string, evs []asrs.Event, _ time.Time) error {
	r.events = append(r.events, evs...)
	return nil
}

func world() World {
	return World{
		Robots: []RobotSpec{{Manufacturer: "EMU", SerialNumber: "R1", MapID: "S1", Speed: 1, ActionSeconds: 1}},
		Asrs: []AsrsSpec{{ID: "ASRS1", Aisles: 1, Levels: 2, Slots: 5, BaseSeconds: 1, Ports: []PortSpec{
			{ID: "IN1", Aisle: 1, Kind: "INPUT", NodeID: "in-node"},
			{ID: "OUT1", Aisle: 1, Kind: "OUTPUT", NodeID: "out-node"},
		}}},
	}
}

func newEngine(t *testing.T) (*Engine, *recorder) {
	t.Helper()
	clock, robots, systems, nodes, err := world().Build(time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	return New(clock, robots, systems, nodes, rec, rec), rec
}

func TestAnnounceAndPublishOnChangeOnly(t *testing.T) {
	e, rec := newEngine(t)
	ctx := context.Background()
	if err := e.Announce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rec.connections) != 1 || rec.connections[0] != "R1:ONLINE" || rec.factsheets != 1 || len(rec.states) != 1 {
		t.Fatalf("announce: %+v", rec)
	}
	_ = e.Step(ctx, time.Second)
	if len(rec.states) != 1 {
		t.Fatalf("idle robot should not republish: %d", len(rec.states))
	}
}

func TestRobotDropsContainerOntoInputPortAndAsrsStoresIt(t *testing.T) {
	e, rec := newEngine(t)
	ctx := context.Background()
	o := robot.Order{ID: "o1", Nodes: []robot.Node{
		{ID: "start", Released: true, Actions: []robot.Action{{ID: "p", Type: "pick", Params: map[string]string{"loadId": "C1"}}}},
		{ID: "in-node", Seq: 2, Released: true, Pos: robot.Position{X: 3}, Actions: []robot.Action{{ID: "d", Type: "drop"}}},
	}, Edges: []robot.Edge{{ID: "e", Seq: 1, Released: true, StartNodeID: "start", EndNodeID: "in-node"}}}
	if err := e.HandleOrder(ctx, "R1", o); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		_ = e.Step(ctx, time.Second)
	}
	if v, _ := e.Asrs("ASRS1"); v.Ports["IN1"] != "C1" {
		t.Fatalf("container should be on IN1: %+v", v)
	}
	if err := e.SubmitJob("ASRS1", asrs.Job{ID: "j1", Kind: asrs.KindStore, ContainerID: "C1", Port: "IN1",
		To: asrs.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_ = e.Step(ctx, time.Second)
	}
	done := false
	for _, ev := range rec.events {
		if ev.Kind == asrs.EventJobDone && ev.JobID == "j1" {
			done = true
		}
	}
	if !done {
		t.Fatalf("events = %+v", rec.events)
	}
}

func TestPickAtEmptyOutputPortFails(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	o := robot.Order{ID: "o1", Nodes: []robot.Node{{ID: "out-node", Released: true,
		Actions: []robot.Action{{ID: "p", Type: "pick", Params: map[string]string{"loadId": "C9"}}}}}}
	_ = e.HandleOrder(ctx, "R1", o)
	_ = e.Step(ctx, 2*time.Second)
	s := e.Robots()[0]
	if s.ActionStates[0].Status != robot.ActionFailed || s.Errors[0].Type != robot.ErrLoadNotAvailable {
		t.Fatalf("state = %+v", s)
	}
}

func TestFaultsAndUnknownTargets(t *testing.T) {
	e, rec := newEngine(t)
	ctx := context.Background()
	before := len(rec.states)
	if err := e.InjectFault(ctx, Fault{Kind: FaultRobotDisconnect, Target: "R1", Active: true}); err != nil {
		t.Fatal(err)
	}
	if rec.connections[len(rec.connections)-1] != "R1:CONNECTION_BROKEN" {
		t.Fatalf("connections = %v", rec.connections)
	}
	_ = e.InjectFault(ctx, Fault{Kind: FaultRobotBattery, Target: "R1", Value: 10})
	_ = e.Step(ctx, time.Second)
	if len(rec.states) != before {
		t.Fatalf("state published while disconnected: %d -> %d", before, len(rec.states))
	}
	_ = e.InjectFault(ctx, Fault{Kind: FaultRobotDisconnect, Target: "R1", Active: false})
	if len(rec.states) != before+1 || rec.states[before].StateOfCharge != 10 {
		t.Fatal("reconnect should publish the current state")
	}
	if err := e.InjectFault(ctx, Fault{Kind: FaultAsrsUnit, Target: "ASRS1", Aisle: 1, Active: true}); err != nil {
		t.Fatal(err)
	}
	_ = e.Step(ctx, time.Second)
	held := false
	for _, ev := range rec.events {
		if ev.Kind == asrs.EventUnitState && ev.State == asrs.UnitHeld {
			held = true
		}
	}
	if !held {
		t.Fatalf("expected a HELD unit state: %+v", rec.events)
	}
	if err := e.HandleOrder(ctx, "NOPE", robot.Order{}); !errors.Is(err, ErrUnknownRobot) {
		t.Fatalf("err = %v", err)
	}
	if err := e.SubmitJob("NOPE", asrs.Job{}); !errors.Is(err, ErrUnknownAsrs) {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildRejectsNonPositiveSpeed(t *testing.T) {
	w := World{Robots: []RobotSpec{{SerialNumber: "R1"}}}
	if _, _, _, _, err := w.Build(time.Now()); err == nil {
		t.Fatal("expected error for zero speed")
	}
}

func moveOrder() robot.Order {
	return robot.Order{ID: "o1", Nodes: []robot.Node{
		{ID: "start", Released: true},
		{ID: "far", Seq: 2, Released: true, Pos: robot.Position{X: 5}},
	}, Edges: []robot.Edge{{ID: "e", Seq: 1, Released: true, StartNodeID: "start", EndNodeID: "far"}}}
}

func TestBlockedRobotDoesNotMove(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()
	if err := e.InjectFault(ctx, Fault{Kind: FaultRobotBlocked, Target: "R1", Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.HandleOrder(ctx, "R1", moveOrder()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_ = e.Step(ctx, time.Second)
	}
	s := e.Robots()[0]
	if s.Position.X != 0 {
		t.Fatalf("blocked robot moved: %+v", s.Position)
	}
	found := false
	for _, er := range s.Errors {
		if er.Type == robot.ErrRobotBlocked {
			found = true
		}
	}
	if !found {
		t.Fatalf("errors = %+v", s.Errors)
	}
}

func TestNoReadOnOutputPortYieldsEmptyContainerID(t *testing.T) {
	e, rec := newEngine(t)
	ctx := context.Background()
	from := asrs.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}
	if err := e.Seed("ASRS1", from, "C5"); err != nil {
		t.Fatal(err)
	}
	if err := e.InjectFault(ctx, Fault{Kind: FaultAsrsNoRead, Target: "ASRS1", Port: "OUT1", Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.SubmitJob("ASRS1", asrs.Job{ID: "j2", Kind: asrs.KindRetrieve, ContainerID: "C5", Port: "OUT1", From: from}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		_ = e.Step(ctx, time.Second)
	}
	for _, ev := range rec.events {
		if ev.Kind == asrs.EventPortArrival && ev.PortID == "OUT1" {
			if ev.ContainerID != "" {
				t.Fatalf("expected no-read, got %+v", ev)
			}
			return
		}
	}
	t.Fatalf("no PORT_ARRIVAL: %+v", rec.events)
}

func TestHandleOrderRejectionStaysReachable(t *testing.T) {
	e, _ := newEngine(t)
	o := moveOrder()
	o.Nodes[0].ID = "elsewhere"
	o.Nodes[0].Pos = robot.Position{X: 50}
	o.Edges[0].StartNodeID = "elsewhere"
	err := e.HandleOrder(context.Background(), "R1", o)
	var re *robot.RejectError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v", err)
	}
}

func TestAnnounceSkipsFaultDisconnectedRobots(t *testing.T) {
	e, rec := newEngine(t)
	ctx := context.Background()
	if err := e.InjectFault(ctx, Fault{Kind: FaultRobotDisconnect, Target: "R1", Active: true}); err != nil {
		t.Fatal(err)
	}
	conns, sheets, states := len(rec.connections), rec.factsheets, len(rec.states)
	if err := e.Announce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rec.connections) != conns || rec.factsheets != sheets || len(rec.states) != states {
		t.Fatalf("announce published for a disconnected robot: %+v", rec)
	}
}

func TestEngineAsrsPassThroughsAndUnknownTargets(t *testing.T) {
	e, rec := newEngine(t)
	ctx := context.Background()
	if err := e.Seed("ASRS1", asrs.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}, "C1"); err != nil {
		t.Fatal(err)
	}
	if err := e.PlaceAtPort("ASRS1", "IN1", "C2"); err != nil {
		t.Fatal(err)
	}
	if err := e.UnitStates(ctx, "ASRS1"); err != nil || len(rec.events) != 1 {
		t.Fatalf("unit states: err=%v events=%d", err, len(rec.events))
	}
	if err := e.CancelJob("ASRS1", "nope"); err == nil {
		t.Fatal("cancelling an unknown job must fail")
	}
	for name, err := range map[string]error{
		"seed":   e.Seed("X", asrs.Position{}, "C"),
		"place":  e.PlaceAtPort("X", "IN1", "C"),
		"cancel": e.CancelJob("X", "j"),
		"states": e.UnitStates(ctx, "X"),
	} {
		if !errors.Is(err, ErrUnknownAsrs) {
			t.Errorf("%s: want ErrUnknownAsrs, got %v", name, err)
		}
	}
	if _, ok := e.Asrs("X"); ok {
		t.Fatal("unknown AS/RS must not be found")
	}
	if e.Now().IsZero() {
		t.Fatal("Now must return the simulated time")
	}
}

func TestHandleInstantActionsPublishesFactsheetAndState(t *testing.T) {
	e, rec := newEngine(t)
	ctx := context.Background()
	err := e.HandleInstantActions(ctx, "R1", []robot.Action{{ID: "a1", Type: "factsheetRequest"}, {ID: "a2", Type: "startCharging"}})
	if err != nil {
		t.Fatal(err)
	}
	if rec.factsheets != 1 || len(rec.states) == 0 {
		t.Fatalf("factsheets=%d states=%d", rec.factsheets, len(rec.states))
	}
	if err := e.HandleInstantActions(ctx, "NOPE", nil); !errors.Is(err, ErrUnknownRobot) {
		t.Fatalf("want ErrUnknownRobot, got %v", err)
	}
}
