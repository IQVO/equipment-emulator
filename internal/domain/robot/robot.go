package robot

import (
	"fmt"
	"math"
	"sort"
	"time"
)

var nodeActionTypes = map[string]bool{"pick": true, "drop": true, "startCharging": true, "stopCharging": true}

// Robot is one simulated AMR. It is not safe for concurrent use; the engine serialises access.
type Robot struct {
	cfg Config

	pos      Position
	order    *Order
	orderID  string // kept after cancel, as VDA 5050 requires
	updateID uint32
	next     int  // index of the node the robot stands on or drives away from
	atNode   bool // true: standing on order.Nodes[next]

	actionIdx     int
	actionElapsed time.Duration
	actionStates  map[string]ActionState
	instantStates map[string]ActionState

	lastNodeID string
	lastSeq    uint32
	driving    bool
	soc        float64
	charging   bool
	load       string
	blocked    bool
	errs       []Error
}

// New creates a robot at cfg.Start with a full battery.
func New(cfg Config) *Robot {
	return &Robot{cfg: cfg, pos: cfg.Start, soc: 100,
		actionStates: map[string]ActionState{}, instantStates: map[string]ActionState{}}
}

// Serial returns the robot's serial number.
func (r *Robot) Serial() string { return r.cfg.SerialNumber }

// SetBlocked injects or clears an obstacle fault.
func (r *Robot) SetBlocked(on bool) { r.blocked = on }

// SetStateOfCharge sets the battery level (clamped to 0..100).
func (r *Robot) SetStateOfCharge(v float64) { r.soc = math.Max(0, math.Min(100, v)) }

// AcceptOrder applies a new order or an order update, following VDA 5050's
// acceptance rules. A rejection is also recorded as an active error.
func (r *Robot) AcceptOrder(o Order) error {
	if err := validate(o); err != nil {
		return r.reject(err)
	}
	if r.order != nil && o.ID == r.order.ID {
		switch {
		case o.UpdateID < r.order.UpdateID:
			return r.reject(&RejectError{ErrOutdatedUpdate, fmt.Sprintf("update %d is older than %d", o.UpdateID, r.order.UpdateID)})
		case o.UpdateID == r.order.UpdateID:
			return r.reject(&RejectError{ErrSameUpdateID, fmt.Sprintf("update %d already applied", o.UpdateID)})
		}
		return r.applyUpdate(o)
	}
	if r.order != nil && !r.finished() {
		return r.reject(&RejectError{ErrInvalidOrder, "the current order is not finished"})
	}
	first := o.Nodes[0]
	if first.ID != r.lastNodeID && dist(r.pos, first.Pos) > r.cfg.PositionTolerance {
		return r.reject(&RejectError{ErrInvalidOrder, "the first node is not the robot's position"})
	}
	r.start(o)
	return nil
}

func validate(o Order) error {
	if o.ID == "" || len(o.Nodes) == 0 {
		return &RejectError{ErrValidation, "an order needs an orderId and at least one node"}
	}
	if len(o.Edges) != len(o.Nodes)-1 {
		return &RejectError{ErrValidation, "an order needs one edge between each pair of nodes"}
	}
	if !o.Nodes[0].Released {
		return &RejectError{ErrValidation, "the first node must be released"}
	}
	horizon := false
	for i, n := range o.Nodes {
		if !n.Released {
			horizon = true
		} else if horizon {
			return &RejectError{ErrValidation, "a released node follows the horizon"}
		}
		for _, a := range n.Actions {
			if !nodeActionTypes[a.Type] {
				return &RejectError{ErrValidation, "unsupported action type " + a.Type}
			}
		}
		if i > 0 {
			e := o.Edges[i-1]
			if e.StartNodeID != o.Nodes[i-1].ID || e.EndNodeID != n.ID {
				return &RejectError{ErrValidation, "edge " + e.ID + " does not join its neighbouring nodes"}
			}
		}
	}
	return nil
}

func (r *Robot) reject(err error) error {
	re := err.(*RejectError)
	r.errs = append(r.errs, Error{Type: re.Type, Level: LevelWarning, Description: re.Reason})
	return err
}

func (r *Robot) start(o Order) {
	cp := cloneOrder(o)
	r.order, r.orderID, r.updateID = &cp, o.ID, o.UpdateID
	r.next, r.atNode, r.actionIdx, r.actionElapsed = 0, true, 0, 0
	r.actionStates = map[string]ActionState{}
	r.errs = nil
	r.registerActions(cp.Nodes)
	r.traverse(0)
}

func (r *Robot) applyUpdate(o Order) error {
	end := r.baseEnd()
	cur := r.order.Nodes[end]
	if o.Nodes[0].ID != cur.ID || o.Nodes[0].Seq != cur.Seq {
		return r.reject(&RejectError{ErrValidation, "an update must start at the last released node " + cur.ID})
	}
	nodes := append(append([]Node{}, r.order.Nodes[:end+1]...), cloneOrder(o).Nodes[1:]...)
	edges := append(append([]Edge{}, r.order.Edges[:end]...), o.Edges...)
	r.order.Nodes, r.order.Edges, r.order.UpdateID = nodes, edges, o.UpdateID
	r.updateID = o.UpdateID
	r.registerActions(nodes[end+1:])
	r.errs = nil
	return nil
}

func (r *Robot) registerActions(nodes []Node) {
	for _, n := range nodes {
		for _, a := range n.Actions {
			r.actionStates[a.ID] = ActionState{ID: a.ID, Type: a.Type, Status: ActionWaiting}
		}
	}
}

func (r *Robot) baseEnd() int {
	end := 0
	for i, n := range r.order.Nodes {
		if n.Released {
			end = i
		}
	}
	return end
}

func (r *Robot) traverse(i int) {
	n := r.order.Nodes[i]
	r.lastNodeID, r.lastSeq, r.pos = n.ID, n.Seq, n.Pos
}

func (r *Robot) finished() bool {
	if r.order == nil {
		return true
	}
	last := len(r.order.Nodes) - 1
	return r.atNode && r.next == last && r.actionIdx >= len(r.order.Nodes[last].Actions)
}

// Tick advances the robot by dt of simulated time.
func (r *Robot) Tick(dt time.Duration, p Peripherals) {
	if r.charging {
		r.soc = math.Min(100, r.soc+r.cfg.ChargePerSecond*dt.Seconds())
	}
	r.driving = false
	if r.order == nil || r.blocked || r.soc <= 0 || r.charging {
		return
	}
	remaining := dt
	for remaining > 0 {
		if r.atNode {
			if !r.runActions(&remaining, p) {
				return
			}
			if r.next+1 >= len(r.order.Nodes) || !r.order.Nodes[r.next+1].Released {
				return
			}
			r.atNode = false
			continue
		}
		if !r.drive(&remaining) {
			return
		}
	}
}

// drive moves along the edge to the next node. It returns true when the robot
// arrived and time remains for further work.
func (r *Robot) drive(remaining *time.Duration) bool {
	target := r.order.Nodes[r.next+1]
	speed := r.cfg.Speed
	if e := r.order.Edges[r.next]; e.MaxSpeed > 0 && e.MaxSpeed < speed {
		speed = e.MaxSpeed
	}
	d := dist(r.pos, target.Pos)
	if r.cfg.DrainPerMeter > 0 {
		d = math.Min(d, r.soc/r.cfg.DrainPerMeter) // cannot drive further than the battery allows
	}
	step := speed * remaining.Seconds()
	r.driving = true
	if step < d {
		r.moveToward(target.Pos, step)
		r.drain(step)
		return false
	}
	r.moveToward(target.Pos, d)
	r.drain(d)
	*remaining -= time.Duration(d / speed * float64(time.Second))
	if r.soc <= 0 {
		r.driving = false
		return false
	}
	r.next++
	r.driving = false // standing on the node until the next edge starts
	r.atNode, r.actionIdx, r.actionElapsed = true, 0, 0
	r.traverse(r.next)
	return true
}

// runActions runs the current node's actions in order. It returns true when
// all of them have finished.
func (r *Robot) runActions(remaining *time.Duration, p Peripherals) bool {
	n := r.order.Nodes[r.next]
	for r.actionIdx < len(n.Actions) {
		a := n.Actions[r.actionIdx]
		st := r.actionStates[a.ID]
		if st.Status == ActionFailed {
			return false
		}
		st.Status = ActionRunning
		r.actionStates[a.ID] = st
		need := r.cfg.ActionDuration - r.actionElapsed
		if *remaining < need {
			r.actionElapsed += *remaining
			*remaining = 0
			return false
		}
		*remaining -= need
		if err := r.complete(a, n.ID, p); err != nil {
			st.Status = ActionFailed
			r.actionStates[a.ID] = st
			r.errs = append(r.errs, Error{Type: ErrLoadNotAvailable, Level: LevelWarning, Description: err.Error()})
			return false
		}
		st.Status = ActionFinished
		r.actionStates[a.ID] = st
		r.actionIdx++
		r.actionElapsed = 0
	}
	return true
}

func (r *Robot) complete(a Action, nodeID string, p Peripherals) error {
	switch a.Type {
	case "pick":
		if r.load != "" {
			return fmt.Errorf("already carrying %s", r.load)
		}
		loadID := a.Params["loadId"]
		if err := p.Pick(nodeID, loadID); err != nil {
			return err
		}
		r.load = loadID
	case "drop":
		if r.load == "" {
			return fmt.Errorf("nothing to drop at %s", nodeID)
		}
		if err := p.Drop(nodeID, r.load); err != nil {
			return err
		}
		r.load = ""
	case "startCharging":
		r.charging = true
	case "stopCharging":
		r.charging = false
	}
	return nil
}

func (r *Robot) moveToward(to Position, step float64) {
	d := dist(r.pos, to)
	if d == 0 || step >= d {
		r.pos = to
		return
	}
	f := step / d
	r.pos.X += (to.X - r.pos.X) * f
	r.pos.Y += (to.Y - r.pos.Y) * f
	r.pos.Theta = math.Atan2(to.Y-r.pos.Y, to.X-r.pos.X)
}

func (r *Robot) drain(m float64) {
	r.soc = math.Max(0, r.soc-r.cfg.DrainPerMeter*m)
}

// InstantAction executes a VDA 5050 instant action.
func (r *Robot) InstantAction(a Action) error {
	st := ActionState{ID: a.ID, Type: a.Type, Status: ActionFinished}
	var err error
	switch a.Type {
	case "cancelOrder":
		if r.order == nil {
			err = &RejectError{ErrNoOrderToCancel, "no active order"}
			break
		}
		for id, s := range r.actionStates {
			if s.Status != ActionFinished {
				s.Status = ActionFailed
				r.actionStates[id] = s
			}
		}
		r.order = nil // the robot stops where it is; orderID and lastNodeID are kept
		r.driving = false
	case "startCharging":
		r.charging = true
	case "stopCharging":
		r.charging = false
	case "stateRequest", "factsheetRequest":
	default:
		err = &RejectError{ErrInvalidInstantAction, "unsupported instant action " + a.Type}
	}
	if err != nil {
		st.Status = ActionFailed
		r.instantStates[a.ID] = st
		return r.reject(err)
	}
	r.instantStates[a.ID] = st
	return nil
}

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
