package asrs

import (
	"fmt"
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

// SetNoRead switches the barcode no-read fault of an output port. Only a
// RETRIEVE's arrival at an output port is read, so an input port is refused
// with WRONG_PORT_KIND rather than accepted for a fault that never fires.
func (s *System) SetNoRead(portID string, on bool) error {
	if _, err := s.port(portID, PortOutput); err != nil {
		return err
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
