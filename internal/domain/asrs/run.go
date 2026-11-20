package asrs

import "time"

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
