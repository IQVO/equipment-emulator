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
	if err := e.InjectFault(ctx, Fault{Kind: FaultRobotDisconnect, Target: "R1", Active: true}); err != nil {
		t.Fatal(err)
	}
	if rec.connections[len(rec.connections)-1] != "R1:CONNECTION_BROKEN" {
		t.Fatalf("connections = %v", rec.connections)
	}
	_ = e.InjectFault(ctx, Fault{Kind: FaultRobotBattery, Target: "R1", Value: 10})
	_ = e.Step(ctx, time.Second)
	n := len(rec.states)
	_ = e.InjectFault(ctx, Fault{Kind: FaultRobotDisconnect, Target: "R1", Active: false})
	if len(rec.states) != n+1 || rec.states[n].StateOfCharge != 10 {
		t.Fatal("reconnect should publish the current state")
	}
	if err := e.InjectFault(ctx, Fault{Kind: FaultAsrsUnit, Target: "ASRS1", Aisle: 1, Active: true}); err != nil {
		t.Fatal(err)
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
