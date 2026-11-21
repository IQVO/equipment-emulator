package robot

import "fmt"

// AcceptOrder applies a new order or an order update, following VDA 5050's
// acceptance rules. A rejection is also recorded as an active error.
func (r *Robot) AcceptOrder(o Order) error {
	if err := validate(o); err != nil {
		return r.reject(err)
	}
	if r.order != nil && o.ID == r.order.ID {
		if o.UpdateID < r.order.UpdateID {
			return r.reject(&RejectError{ErrOutdatedUpdate, fmt.Sprintf("update %d is older than %d", o.UpdateID, r.order.UpdateID)})
		}
		if o.UpdateID == r.order.UpdateID {
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
	r.addError(Error{Type: re.Type, Level: LevelWarning, Description: re.Reason})
	return err
}

func (r *Robot) start(o Order) {
	cp := cloneOrder(o)
	r.order, r.orderID, r.updateID = &cp, o.ID, o.UpdateID
	r.next, r.atNode, r.actionIdx, r.actionElapsed = 0, true, 0, 0
	r.actionStates = map[string]ActionState{}
	r.instantStates, r.instantOrder = map[string]ActionState{}, nil // a new order starts a clean report
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
