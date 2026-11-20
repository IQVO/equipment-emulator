package asrs

import "sort"

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
