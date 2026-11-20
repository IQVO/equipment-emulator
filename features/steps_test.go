package features

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

type rec struct{ events []asrs.Event }

func (r *rec) PublishState(context.Context, robot.State, time.Time) error                 { return nil }
func (r *rec) PublishConnection(context.Context, string, string, string, time.Time) error { return nil }
func (r *rec) PublishFactsheet(context.Context, robot.Factsheet, time.Time) error         { return nil }
func (r *rec) PublishEvents(_ context.Context, _ string, evs []asrs.Event, _ time.Time) error {
	r.events = append(r.events, evs...)
	return nil
}

type world struct {
	spec engine.World
	eng  *engine.Engine
	rec  *rec
	ctx  context.Context
}

func (w *world) build() error {
	if w.eng != nil {
		return nil
	}
	clock, robots, systems, nodes, err := w.spec.Build(time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC))
	if err != nil {
		return err
	}
	w.rec = &rec{}
	w.eng = engine.New(clock, robots, systems, nodes, w.rec, w.rec)
	return nil
}

func (w *world) aRobotAt(serial string, x float64) error {
	w.spec.Robots = append(w.spec.Robots, engine.RobotSpec{Manufacturer: "EMU", SerialNumber: serial, StartX: x, MapID: "S1", Speed: 1, ActionSeconds: 1})
	return nil
}

func (w *world) anOrder(id string, from, to float64) error {
	if err := w.build(); err != nil {
		return err
	}
	o := robot.Order{ID: id, Nodes: []robot.Node{
		{ID: "a", Released: true, Pos: robot.Position{X: from, MapID: "S1"}},
		{ID: "b", Seq: 2, Released: true, Pos: robot.Position{X: to, MapID: "S1"}},
	}, Edges: []robot.Edge{{ID: "e", Seq: 1, Released: true, StartNodeID: "a", EndNodeID: "b"}}}
	return w.eng.HandleOrder(w.ctx, w.spec.Robots[0].SerialNumber, o)
}

func (w *world) blocked(serial string, on bool) error {
	return w.eng.InjectFault(w.ctx, engine.Fault{Kind: engine.FaultRobotBlocked, Target: serial, Active: on})
}

func (w *world) secondsPass(n int) error {
	if err := w.build(); err != nil {
		return err
	}
	for i := 0; i < n*10; i++ {
		_ = w.eng.Step(w.ctx, 100*time.Millisecond)
	}
	return nil
}

func (w *world) robotState(serial string) (robot.State, error) {
	for _, s := range w.eng.Robots() {
		if s.SerialNumber == serial {
			return s, nil
		}
	}
	return robot.State{}, fmt.Errorf("no robot %s", serial)
}

func (w *world) robotAt(serial string, x float64) error {
	s, err := w.robotState(serial)
	if err != nil {
		return err
	}
	if math.Abs(s.Position.X-x) > 1e-6 {
		return fmt.Errorf("robot %s at x %v, want %v", serial, s.Position.X, x)
	}
	return nil
}

func (w *world) robotError(serial, typ string) error {
	s, err := w.robotState(serial)
	if err != nil {
		return err
	}
	for _, e := range s.Errors {
		if e.Type == typ {
			return nil
		}
	}
	return fmt.Errorf("errors = %+v", s.Errors)
}

func (w *world) anAsrs(id, container string, level, slot int) error {
	w.spec.Asrs = append(w.spec.Asrs, engine.AsrsSpec{ID: id, Aisles: 1, Levels: 4, Slots: 10, BaseSeconds: 5,
		Ports: []engine.PortSpec{{ID: "OUT1", Aisle: 1, Kind: "OUTPUT"}}})
	if err := w.build(); err != nil {
		return err
	}
	return w.eng.Seed(id, asrs.Position{Aisle: 1, Level: level, Slot: slot, Depth: 1}, container)
}

func (w *world) noRead(port string) error {
	return w.eng.InjectFault(w.ctx, engine.Fault{Kind: engine.FaultAsrsNoRead, Target: "ASRS1", Port: port, Active: true})
}

func (w *world) retrieve(job, container string, level, slot int, port string) error {
	return w.eng.SubmitJob("ASRS1", asrs.Job{ID: job, Kind: asrs.KindRetrieve, ContainerID: container, Port: port,
		From: asrs.Position{Aisle: 1, Level: level, Slot: slot, Depth: 1}})
}

func (w *world) aisleFault(aisle int, on bool) error {
	return w.eng.InjectFault(w.ctx, engine.Fault{Kind: engine.FaultAsrsUnit, Target: "ASRS1", Aisle: aisle, Active: on})
}

func (w *world) arrivalWithout(port string) error {
	for _, e := range w.rec.events {
		if e.Kind == asrs.EventPortArrival && e.PortID == port && e.ContainerID == "" {
			return nil
		}
	}
	return fmt.Errorf("events = %+v", w.rec.events)
}

func (w *world) jobDone(job string, want bool) error {
	done := false
	for _, e := range w.rec.events {
		if e.Kind == asrs.EventJobDone && e.JobID == job {
			done = true
		}
	}
	if done != want {
		return fmt.Errorf("job %s done = %v, want %v", job, done, want)
	}
	return nil
}

func initScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = world{ctx: ctx}
		return ctx, nil
	})
	sc.Step(`^a robot "([^"]*)" at x (\d+)$`, w.aRobotAt)
	sc.Step(`^an order "([^"]*)" from x (\d+) to x (\d+)$`, w.anOrder)
	sc.Step(`^robot "([^"]*)" is blocked$`, func(s string) error { return w.blocked(s, true) })
	sc.Step(`^robot "([^"]*)" is unblocked$`, func(s string) error { return w.blocked(s, false) })
	sc.Step(`^(\d+) seconds pass$`, w.secondsPass)
	sc.Step(`^robot "([^"]*)" is at x (\d+)$`, w.robotAt)
	sc.Step(`^robot "([^"]*)" reports error "([^"]*)"$`, w.robotError)
	sc.Step(`^an AS/RS "([^"]*)" with container "([^"]*)" at level (\d+) slot (\d+)$`, w.anAsrs)
	sc.Step(`^a no-read at port "([^"]*)"$`, w.noRead)
	sc.Step(`^job "([^"]*)" retrieves "([^"]*)" from level (\d+) slot (\d+) to port "([^"]*)"$`, w.retrieve)
	sc.Step(`^aisle (\d+) faults$`, func(a int) error { return w.aisleFault(a, true) })
	sc.Step(`^aisle (\d+) recovers$`, func(a int) error { return w.aisleFault(a, false) })
	sc.Step(`^the AS/RS reports a port arrival at "([^"]*)" without a container$`, w.arrivalWithout)
	sc.Step(`^job "([^"]*)" is not done$`, func(j string) error { return w.jobDone(j, false) })
	sc.Step(`^job "([^"]*)" is done$`, func(j string) error { return w.jobDone(j, true) })
}

func TestFeatures(t *testing.T) {
	s := godog.TestSuite{ScenarioInitializer: initScenario, Options: &godog.Options{Format: "pretty", Paths: []string{"."}, TestingT: t, Strict: true}}
	if s.Run() != 0 {
		t.Fatal("feature scenarios failed")
	}
}
