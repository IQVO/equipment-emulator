package robot

import (
	"math"
	"sort"
)

// Snapshot returns the robot's current state.
func (r *Robot) Snapshot() State {
	s := State{
		Manufacturer: r.cfg.Manufacturer, SerialNumber: r.cfg.SerialNumber,
		OrderID: r.orderID, OrderUpdateID: r.updateID,
		LastNodeID: r.lastNodeID, LastNodeSeq: r.lastSeq,
		Position: r.pos, Driving: r.driving,
		StateOfCharge: r.soc, Charging: r.charging, LoadID: r.load,
		Errors: append([]Error{}, r.errs...),
	}
	if r.order != nil {
		for _, n := range r.order.Nodes[r.next+1:] {
			s.NodeStates = append(s.NodeStates, NodeState{ID: n.ID, Seq: n.Seq, Released: n.Released})
		}
		for _, e := range r.order.Edges[r.next:] {
			s.EdgeStates = append(s.EdgeStates, EdgeState{ID: e.ID, Seq: e.Seq, Released: e.Released})
		}
	}
	if r.blocked {
		s.Errors = append(s.Errors, Error{Type: ErrRobotBlocked, Level: LevelCritical, Description: "obstacle on the path"})
	}
	if r.soc <= 0 {
		s.Errors = append(s.Errors, Error{Type: ErrBatteryEmpty, Level: LevelFatal, Description: "battery empty"})
	}
	s.ActionStates = sortedStates(r.actionStates)
	s.InstantActionStates = sortedStates(r.instantStates)
	return s
}

// Factsheet returns the robot's published capabilities.
func (r *Robot) Factsheet() Factsheet {
	return Factsheet{Manufacturer: r.cfg.Manufacturer, SerialNumber: r.cfg.SerialNumber, MaxSpeed: r.cfg.Speed,
		SupportedActions: []string{"pick", "drop", "startCharging", "stopCharging", "cancelOrder", "stateRequest", "factsheetRequest"}}
}

func sortedStates(m map[string]ActionState) []ActionState {
	out := make([]ActionState, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func cloneOrder(o Order) Order {
	cp := Order{ID: o.ID, UpdateID: o.UpdateID, Nodes: make([]Node, len(o.Nodes)), Edges: append([]Edge{}, o.Edges...)}
	for i, n := range o.Nodes {
		n.Actions = append([]Action{}, n.Actions...)
		cp.Nodes[i] = n
	}
	return cp
}

func dist(a, b Position) float64 { return math.Hypot(b.X-a.X, b.Y-a.Y) }
