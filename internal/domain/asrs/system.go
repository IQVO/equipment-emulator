package asrs

import (
	"fmt"
	"sort"
	"time"
)

type unit struct {
	id        string
	state     UnitState
	queue     []Job
	current   *Job
	remaining time.Duration
	faulted   bool
}

// System is one simulated AS/RS. Not safe for concurrent use; the engine serialises access.
type System struct {
	id      string
	layout  Layout
	timing  Timing
	slots   map[Position]string
	ports   map[string]string
	portDef map[string]Port
	units   map[int]*unit
	aisles  []int
	jobs    map[string]bool
	noRead  map[string]bool
}

// New validates the layout and creates an empty AS/RS.
func New(id string, l Layout, t Timing) (*System, error) {
	if l.Aisles < 1 || l.Levels < 1 || l.Slots < 1 {
		return nil, fmt.Errorf("asrs %s: aisles, levels and slots must be positive", id)
	}
	s := &System{id: id, layout: l, timing: t, slots: map[Position]string{}, ports: map[string]string{},
		portDef: map[string]Port{}, units: map[int]*unit{}, jobs: map[string]bool{}, noRead: map[string]bool{}}
	for _, p := range l.Ports {
		if p.Aisle < 1 || p.Aisle > l.Aisles {
			return nil, fmt.Errorf("asrs %s: port %s is outside the aisles", id, p.ID)
		}
		s.portDef[p.ID], s.ports[p.ID] = p, ""
	}
	for a := 1; a <= l.Aisles; a++ {
		s.units[a] = &unit{id: fmt.Sprintf("A%d", a), state: UnitIdle}
		s.aisles = append(s.aisles, a)
	}
	return s, nil
}

func (s *System) ID() string { return s.id }

func (s *System) validPos(p Position) bool {
	maxDepth := 1
	if s.layout.DoubleDeep {
		maxDepth = 2
	}
	return p.Aisle >= 1 && p.Aisle <= s.layout.Aisles && p.Level >= 1 && p.Level <= s.layout.Levels &&
		p.Slot >= 1 && p.Slot <= s.layout.Slots && p.Depth >= 1 && p.Depth <= maxDepth
}

func (s *System) port(id string, kind PortKind) (Port, error) {
	p, ok := s.portDef[id]
	if !ok {
		return Port{}, &RejectError{ReasonUnknownPort}
	}
	if p.Kind != kind {
		return Port{}, &RejectError{ReasonWrongPortKind}
	}
	return p, nil
}

// portJobAisle validates a job that touches a port and one rack position.
func (s *System) portJobAisle(portID string, kind PortKind, pos Position) (int, error) {
	p, err := s.port(portID, kind)
	if err != nil {
		return 0, err
	}
	if !s.validPos(pos) {
		return 0, &RejectError{ReasonInvalidPosition}
	}
	if pos.Aisle != p.Aisle {
		return 0, &RejectError{ReasonAisleMismatch}
	}
	return p.Aisle, nil
}

func (s *System) jobAisle(j Job) (int, error) {
	switch j.Kind {
	case KindStore:
		return s.portJobAisle(j.Port, PortInput, j.To)
	case KindRetrieve:
		return s.portJobAisle(j.Port, PortOutput, j.From)
	case KindMove:
		if !s.validPos(j.From) || !s.validPos(j.To) {
			return 0, &RejectError{ReasonInvalidPosition}
		}
		if j.From.Aisle != j.To.Aisle {
			return 0, &RejectError{ReasonAisleMismatch}
		}
		return j.From.Aisle, nil
	default:
		return 0, &RejectError{ReasonInvalidPosition}
	}
}

// Submit queues a job after the checks the PLC can make at once.
func (s *System) Submit(j Job) error {
	if s.jobs[j.ID] {
		return &RejectError{ReasonDuplicateJob}
	}
	aisle, err := s.jobAisle(j)
	if err != nil {
		return err
	}
	s.jobs[j.ID] = true
	u := s.units[aisle]
	u.queue = append(u.queue, j)
	return nil
}

// Cancel removes a queued job. A running job cannot be cancelled.
func (s *System) Cancel(jobID string) error {
	for _, a := range s.aisles {
		u := s.units[a]
		if u.current != nil && u.current.ID == jobID {
			return &RejectError{ReasonJobRunning}
		}
		for i, j := range u.queue {
			if j.ID == jobID {
				u.queue = append(u.queue[:i], u.queue[i+1:]...)
				return nil
			}
		}
	}
	return &RejectError{ReasonUnknownJob}
}

// PlaceAtPort puts a container on an input port (a robot dropped it there).
func (s *System) PlaceAtPort(portID, containerID string) error {
	if _, err := s.port(portID, PortInput); err != nil {
		return err
	}
	if s.ports[portID] != "" {
		return &RejectError{ReasonPortOccupied}
	}
	s.ports[portID] = containerID
	return nil
}

// TakeFromPort removes a container from an output port (a robot picked it).
func (s *System) TakeFromPort(portID, containerID string) error {
	if _, err := s.port(portID, PortOutput); err != nil {
		return err
	}
	switch s.ports[portID] {
	case "":
		return &RejectError{ReasonPortEmpty}
	case containerID:
		s.ports[portID] = ""
		return nil
	default:
		return &RejectError{ReasonWrongContainer}
	}
}

// Seed places a container in the rack (initial inventory).
func (s *System) Seed(p Position, containerID string) error {
	if !s.validPos(p) {
		return &RejectError{ReasonInvalidPosition}
	}
	if s.slots[p] != "" {
		return &RejectError{ReasonPositionOccupied}
	}
	s.slots[p] = containerID
	return nil
}

func (s *System) SetUnitFault(aisle int, on bool) error {
	u, ok := s.units[aisle]
	if !ok {
		return &RejectError{ReasonInvalidPosition}
	}
	u.faulted = on
	return nil
}

func (s *System) SetNoRead(portID string, on bool) error {
	if _, ok := s.portDef[portID]; !ok {
		return &RejectError{ReasonUnknownPort}
	}
	s.noRead[portID] = on
	return nil
}

func (s *System) duration(j Job) time.Duration {
	p := j.To
	if j.Kind == KindRetrieve || (j.Kind == KindMove && j.From.Level > j.To.Level) {
		p = j.From
	}
	return s.timing.Base + time.Duration(p.Level)*s.timing.PerLevel + time.Duration(p.Slot)*s.timing.PerSlot
}

func (s *System) blocked(p Position) bool { return p.Depth == 2 && s.slots[p.front()] != "" }

// precheck returns the failure reason for a job about to start, or "".
func (s *System) precheck(j Job) string {
	switch j.Kind {
	case KindStore:
		if s.ports[j.Port] != j.ContainerID {
			return ReasonContainerNotAtPort
		}
		if s.slots[j.To] != "" {
			return ReasonPositionOccupied
		}
		if s.blocked(j.To) {
			return ReasonPositionBlocked
		}
	case KindRetrieve:
		if s.slots[j.From] != j.ContainerID {
			return ReasonContainerNotAtPosition
		}
		if s.blocked(j.From) {
			return ReasonPositionBlocked
		}
		if s.ports[j.Port] != "" {
			return ReasonPortOccupied
		}
	case KindMove:
		if s.slots[j.From] != j.ContainerID {
			return ReasonContainerNotAtPosition
		}
		if s.blocked(j.From) || s.blocked(j.To) {
			return ReasonPositionBlocked
		}
		if s.slots[j.To] != "" {
			return ReasonPositionOccupied
		}
	}
	return ""
}

func (s *System) begin(j Job) {
	switch j.Kind {
	case KindStore:
		s.ports[j.Port] = ""
	case KindRetrieve, KindMove:
		delete(s.slots, j.From)
	}
}

func (s *System) finish(j Job) []Event {
	evs := []Event{{Kind: EventJobDone, JobID: j.ID}}
	switch j.Kind {
	case KindStore, KindMove:
		s.slots[j.To] = j.ContainerID
	case KindRetrieve:
		s.ports[j.Port] = j.ContainerID
		seen := j.ContainerID
		if s.noRead[j.Port] {
			seen = ""
		}
		evs = append(evs, Event{Kind: EventPortArrival, PortID: j.Port, ContainerID: seen})
	}
	return evs
}

func (s *System) setState(u *unit, st UnitState, evs []Event) []Event {
	if u.state == st {
		return evs
	}
	u.state = st
	return append(evs, Event{Kind: EventUnitState, UnitID: u.id, State: st})
}

// startNext pops queued jobs until one passes its precheck and starts.
// It returns false when the queue is exhausted (the unit is then idle).
func (s *System) startNext(u *unit, evs []Event) ([]Event, bool) {
	for len(u.queue) > 0 {
		j := u.queue[0]
		u.queue = u.queue[1:]
		if r := s.precheck(j); r != "" {
			evs = append(evs, Event{Kind: EventJobFailed, JobID: j.ID, Reason: r})
			continue
		}
		s.begin(j)
		u.current, u.remaining = &j, s.duration(j)
		return evs, true
	}
	return evs, false
}

// advance runs one unit for dt.
func (s *System) advance(u *unit, dt time.Duration, evs []Event) []Event {
	if u.faulted {
		return s.setState(u, UnitHeld, evs)
	}
	left := dt
	for {
		if u.current == nil {
			var started bool
			if evs, started = s.startNext(u, evs); !started {
				return s.setState(u, UnitIdle, evs)
			}
		}
		evs = s.setState(u, UnitExecute, evs)
		if left < u.remaining {
			u.remaining -= left
			return evs
		}
		left -= u.remaining
		evs = append(evs, s.finish(*u.current)...)
		u.current = nil
	}
}

// Tick advances every aisle unit by dt and returns what the PLC reports.
func (s *System) Tick(dt time.Duration) []Event {
	var evs []Event
	for _, a := range s.aisles {
		evs = s.advance(s.units[a], dt, evs)
	}
	return evs
}

// UnitStates reports every unit's current state (for a STATE_REQUEST).
func (s *System) UnitStates() []Event {
	var evs []Event
	for _, a := range s.aisles {
		u := s.units[a]
		evs = append(evs, Event{Kind: EventUnitState, UnitID: u.id, State: u.state})
	}
	return evs
}

// Snapshot returns a read-only view for the admin API.
func (s *System) Snapshot() View {
	v := View{ID: s.id, Slots: map[string]string{}, Ports: map[string]string{}}
	for p, c := range s.slots {
		v.Slots[p.String()] = c
	}
	for p, c := range s.ports {
		v.Ports[p] = c
	}
	for _, a := range s.aisles {
		u := s.units[a]
		uv := UnitView{ID: u.id, State: u.state, Queued: len(u.queue)}
		if u.current != nil {
			uv.CurrentJob = u.current.ID
		}
		v.Units = append(v.Units, uv)
	}
	sort.Slice(v.Units, func(i, j int) bool { return v.Units[i].ID < v.Units[j].ID })
	return v
}
