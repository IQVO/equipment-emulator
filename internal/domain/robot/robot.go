package robot

import (
	"fmt"
	"math"
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
	instantOrder  []string // instantStates ids, oldest first, for the MaxReported bound

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
	if e := r.order.Edges[r.next]; e.MaxSpeed > 0 { // 0 = no limit
		speed = math.Min(speed, e.MaxSpeed)
	}
	d := dist(r.pos, target.Pos)
	capped := false
	if r.cfg.DrainPerMeter > 0 {
		if reach := r.soc / r.cfg.DrainPerMeter; reach < d { // cannot drive further than the battery allows
			d, capped = reach, true
		}
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
	if capped { // the battery ran dry short of the node: stop here, do not arrive
		r.soc = 0
		r.driving = false
		return false
	}
	*remaining -= time.Duration(d / speed * float64(time.Second))
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
			r.addError(Error{Type: ErrLoadNotAvailable, Level: LevelWarning, Description: err.Error()})
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
		r.setInstantState(st)
		return r.reject(err)
	}
	r.setInstantState(st)
	return nil
}

// setInstantState records an instant action's state, keeping only the most
// recent MaxReported ids (a known id is updated in place).
func (r *Robot) setInstantState(st ActionState) {
	if _, known := r.instantStates[st.ID]; !known {
		r.instantOrder = append(r.instantOrder, st.ID)
		if len(r.instantOrder) > MaxReported {
			delete(r.instantStates, r.instantOrder[0])
			r.instantOrder = r.instantOrder[1:]
		}
	}
	r.instantStates[st.ID] = st
}

// addError records an active error, keeping only the most recent MaxReported.
func (r *Robot) addError(e Error) {
	r.errs = append(r.errs, e)
	if len(r.errs) > MaxReported { // one is added at a time, so dropping the oldest suffices
		r.errs = r.errs[1:]
	}
}
