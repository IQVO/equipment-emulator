// Package engine drives the simulation: it advances simulated time, ticks
// every robot and AS/RS, connects robot pick/drop to AS/RS ports, and
// publishes what changed through the output ports.
package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
	"github.com/IQVO/equipment-emulator/internal/domain/simclock"
)

var (
	ErrUnknownRobot = errors.New("unknown robot")
	ErrUnknownAsrs  = errors.New("unknown asrs")
)

type RobotPublisher interface {
	PublishState(ctx context.Context, s robot.State, at time.Time) error
	PublishConnection(ctx context.Context, manufacturer, serial, state string, at time.Time) error
	PublishFactsheet(ctx context.Context, f robot.Factsheet, at time.Time) error
}

type AsrsPublisher interface {
	PublishEvents(ctx context.Context, asrsID string, evs []asrs.Event, at time.Time) error
}

type PortNode struct{ AsrsID, PortID string }

type FaultKind string

const (
	FaultRobotBlocked    FaultKind = "ROBOT_BLOCKED"
	FaultRobotBattery    FaultKind = "ROBOT_BATTERY"
	FaultRobotDisconnect FaultKind = "ROBOT_DISCONNECT"
	FaultAsrsUnit        FaultKind = "ASRS_UNIT"
	FaultAsrsNoRead      FaultKind = "ASRS_NO_READ"
)

type Fault struct {
	Kind   FaultKind
	Target string // robot serial or asrs id
	Aisle  int
	Port   string
	Active bool
	Value  float64
}

// Connection states, as VDA 5050 names them.
const (
	connOnline = "ONLINE"
	connBroken = "CONNECTION_BROKEN"
)

type Engine struct {
	mu           sync.Mutex
	clock        *simclock.Sim
	robots       map[string]*robot.Robot
	serials      []string
	systems      map[string]*asrs.System
	systemIDs    []string
	portNodes    map[string]PortNode
	rp           RobotPublisher
	ap           AsrsPublisher
	last         map[string]robot.State
	disconnected map[string]bool
}

func New(clock *simclock.Sim, robots []*robot.Robot, systems []*asrs.System, portNodes map[string]PortNode, rp RobotPublisher, ap AsrsPublisher) *Engine {
	e := &Engine{clock: clock, robots: map[string]*robot.Robot{}, systems: map[string]*asrs.System{},
		portNodes: portNodes, rp: rp, ap: ap, last: map[string]robot.State{}, disconnected: map[string]bool{}}
	for _, r := range robots {
		e.robots[r.Serial()] = r
		e.serials = append(e.serials, r.Serial())
	}
	for _, s := range systems {
		e.systems[s.ID()] = s
		e.systemIDs = append(e.systemIDs, s.ID())
	}
	sort.Strings(e.serials)
	sort.Strings(e.systemIDs)
	return e
}

func (e *Engine) Now() time.Time { return e.clock.Now() }

// peripherals connects robot pick/drop at a port node to the AS/RS. Any other node is a station or a dock: always fine.
type peripherals struct{ e *Engine }

func (p peripherals) Pick(nodeID, loadID string) error {
	pn, ok := p.e.portNodes[nodeID]
	if !ok {
		return nil
	}
	return p.e.systems[pn.AsrsID].TakeFromPort(pn.PortID, loadID)
}

func (p peripherals) Drop(nodeID, loadID string) error {
	pn, ok := p.e.portNodes[nodeID]
	if !ok {
		return nil
	}
	return p.e.systems[pn.AsrsID].PlaceAtPort(pn.PortID, loadID)
}

// Announce publishes ONLINE, the factsheet and the state of every robot.
func (e *Engine) Announce(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Now()
	var errs []error
	for _, s := range e.serials {
		r := e.robots[s]
		st := r.Snapshot()
		errs = append(errs, e.rp.PublishConnection(ctx, st.Manufacturer, s, connOnline, now),
			e.rp.PublishFactsheet(ctx, r.Factsheet(), now), e.publishState(ctx, s, true, now))
	}
	return errors.Join(errs...)
}

func (e *Engine) publishState(ctx context.Context, serial string, force bool, now time.Time) error {
	if e.disconnected[serial] {
		return nil
	}
	st := e.robots[serial].Snapshot()
	if !force && reflect.DeepEqual(st, e.last[serial]) {
		return nil
	}
	e.last[serial] = st
	return e.rp.PublishState(ctx, st, now)
}

// Step advances the simulation by dt of simulated time.
func (e *Engine) Step(ctx context.Context, dt time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Advance(dt)
	var errs []error
	for _, s := range e.serials {
		e.robots[s].Tick(dt, peripherals{e})
		errs = append(errs, e.publishState(ctx, s, false, now))
	}
	for _, id := range e.systemIDs {
		if evs := e.systems[id].Tick(dt); len(evs) > 0 {
			errs = append(errs, e.ap.PublishEvents(ctx, id, evs, now))
		}
	}
	return errors.Join(errs...)
}

func (e *Engine) robot(serial string) (*robot.Robot, error) {
	r, ok := e.robots[serial]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRobot, serial)
	}
	return r, nil
}

func (e *Engine) system(id string) (*asrs.System, error) {
	s, ok := e.systems[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownAsrs, id)
	}
	return s, nil
}

// HandleOrder applies a VDA 5050 order and publishes the resulting state at once.
func (e *Engine) HandleOrder(ctx context.Context, serial string, o robot.Order) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, err := e.robot(serial)
	if err != nil {
		return err
	}
	accErr := r.AcceptOrder(o)
	return errors.Join(accErr, e.publishState(ctx, serial, true, e.clock.Now()))
}

// HandleInstantActions runs instant actions; stateRequest and factsheetRequest publish at once.
func (e *Engine) HandleInstantActions(ctx context.Context, serial string, as []robot.Action) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, err := e.robot(serial)
	if err != nil {
		return err
	}
	now := e.clock.Now()
	var errs []error
	for _, a := range as {
		errs = append(errs, r.InstantAction(a))
		if a.Type == "factsheetRequest" {
			errs = append(errs, e.rp.PublishFactsheet(ctx, r.Factsheet(), now))
		}
	}
	errs = append(errs, e.publishState(ctx, serial, true, now))
	return errors.Join(errs...)
}

func (e *Engine) SubmitJob(asrsID string, j asrs.Job) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.system(asrsID)
	if err != nil {
		return err
	}
	return s.Submit(j)
}

func (e *Engine) CancelJob(asrsID, jobID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.system(asrsID)
	if err != nil {
		return err
	}
	return s.Cancel(jobID)
}

// UnitStates publishes every unit's state (the answer to a STATE_REQUEST).
func (e *Engine) UnitStates(ctx context.Context, asrsID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.system(asrsID)
	if err != nil {
		return err
	}
	return e.ap.PublishEvents(ctx, asrsID, s.UnitStates(), e.clock.Now())
}

func (e *Engine) Seed(asrsID string, p asrs.Position, containerID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.system(asrsID)
	if err != nil {
		return err
	}
	return s.Seed(p, containerID)
}

func (e *Engine) PlaceAtPort(asrsID, portID, containerID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, err := e.system(asrsID)
	if err != nil {
		return err
	}
	return s.PlaceAtPort(portID, containerID)
}

// InjectFault switches a fault on or off.
func (e *Engine) InjectFault(ctx context.Context, f Fault) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.clock.Now()
	switch f.Kind {
	case FaultRobotBlocked, FaultRobotBattery, FaultRobotDisconnect:
		r, err := e.robot(f.Target)
		if err != nil {
			return err
		}
		switch f.Kind {
		case FaultRobotBlocked:
			r.SetBlocked(f.Active)
		case FaultRobotBattery:
			r.SetStateOfCharge(f.Value)
		case FaultRobotDisconnect:
			man := r.Snapshot().Manufacturer
			if f.Active {
				e.disconnected[f.Target] = true
				return e.rp.PublishConnection(ctx, man, f.Target, connBroken, now)
			}
			delete(e.disconnected, f.Target)
			return errors.Join(e.rp.PublishConnection(ctx, man, f.Target, connOnline, now), e.publishState(ctx, f.Target, true, now))
		}
		return nil
	case FaultAsrsUnit, FaultAsrsNoRead:
		s, err := e.system(f.Target)
		if err != nil {
			return err
		}
		if f.Kind == FaultAsrsUnit {
			return s.SetUnitFault(f.Aisle, f.Active)
		}
		return s.SetNoRead(f.Port, f.Active)
	default:
		return fmt.Errorf("unknown fault kind %q", f.Kind)
	}
}

func (e *Engine) Robots() []robot.State {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]robot.State, 0, len(e.serials))
	for _, s := range e.serials {
		out = append(out, e.robots[s].Snapshot())
	}
	return out
}

func (e *Engine) Asrs(id string) (asrs.View, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.systems[id]
	if !ok {
		return asrs.View{}, false
	}
	return s.Snapshot(), true
}
