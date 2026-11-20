package vda5050

import (
	"errors"
	"fmt"
	"time"

	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

func NewHeader(id uint32, at time.Time, manufacturer, serial string) Header {
	return Header{HeaderID: id, Timestamp: at.UTC().Format(TimeLayout), Version: Version, Manufacturer: manufacturer, SerialNumber: serial}
}

// ToOrder maps a wire order to the robot model. The emulator requires a
// nodePosition on every node and supports no edge actions.
func ToOrder(o Order) (robot.Order, error) {
	d := robot.Order{ID: o.OrderID, UpdateID: o.OrderUpdateID}
	for _, n := range o.Nodes {
		if n.NodePosition == nil {
			return robot.Order{}, fmt.Errorf("node %s has no nodePosition", n.NodeID)
		}
		p := robot.Position{X: n.NodePosition.X, Y: n.NodePosition.Y, MapID: n.NodePosition.MapID}
		if n.NodePosition.Theta != nil {
			p.Theta = *n.NodePosition.Theta
		}
		d.Nodes = append(d.Nodes, robot.Node{ID: n.NodeID, Seq: n.SequenceID, Released: n.Released, Pos: p, Actions: ToActions(n.Actions)})
	}
	for _, e := range o.Edges {
		if len(e.Actions) > 0 {
			return robot.Order{}, errors.New("edge actions are not supported by the emulator")
		}
		de := robot.Edge{ID: e.EdgeID, Seq: e.SequenceID, Released: e.Released, StartNodeID: e.StartNodeID, EndNodeID: e.EndNodeID}
		if e.MaxSpeed != nil {
			de.MaxSpeed = *e.MaxSpeed
		}
		d.Edges = append(d.Edges, de)
	}
	return d, nil
}

// ToActions maps wire actions; parameter values are rendered as strings.
func ToActions(as []Action) []robot.Action {
	out := make([]robot.Action, 0, len(as))
	for _, a := range as {
		params := map[string]string{}
		for _, p := range a.ActionParameters {
			params[p.Key] = fmt.Sprint(p.Value)
		}
		out = append(out, robot.Action{ID: a.ActionID, Type: a.ActionType, Params: params})
	}
	return out
}

func states(in []robot.ActionState) []ActionState {
	out := make([]ActionState, 0, len(in))
	for _, s := range in {
		out = append(out, ActionState{ActionID: s.ID, ActionType: s.Type, ActionStatus: string(s.Status)})
	}
	return out
}

// FromState maps the robot snapshot to a VDA 5050 v3 state message.
func FromState(s robot.State, h Header) State {
	w := State{Header: h, OrderID: s.OrderID, OrderUpdateID: s.OrderUpdateID,
		LastNodeID: s.LastNodeID, LastNodeSequenceID: s.LastNodeSeq,
		NodeStates: []NodeState{}, EdgeStates: []EdgeState{},
		MobileRobotPosition: &MobileRobotPosition{X: s.Position.X, Y: s.Position.Y, Theta: s.Position.Theta, MapID: s.Position.MapID, Localized: true},
		Driving:             s.Driving,
		ActionStates:        states(s.ActionStates), InstantActionStates: states(s.InstantActionStates),
		PowerSupply:   PowerSupply{StateOfCharge: s.StateOfCharge, Charging: s.Charging},
		OperatingMode: "AUTOMATIC", Errors: []Error{}, Information: []any{},
		SafetyState: SafetyState{ActiveEmergencyStop: "NONE"}}
	for _, n := range s.NodeStates {
		w.NodeStates = append(w.NodeStates, NodeState{NodeID: n.ID, SequenceID: n.Seq, Released: n.Released})
	}
	for _, e := range s.EdgeStates {
		w.EdgeStates = append(w.EdgeStates, EdgeState{EdgeID: e.ID, SequenceID: e.Seq, Released: e.Released})
	}
	for _, e := range s.Errors {
		w.Errors = append(w.Errors, Error{ErrorType: e.Type, ErrorLevel: string(e.Level), ErrorDescription: e.Description})
	}
	if s.LoadID != "" {
		w.Loads = []Load{{LoadID: s.LoadID}}
	}
	return w
}

// FromFactsheet maps the robot's capabilities to the factsheet subset.
func FromFactsheet(f robot.Factsheet, h Header) Factsheet {
	return Factsheet{Header: h,
		TypeSpecification:  TypeSpecification{SeriesName: "EMU-AMR", MaximumLoadMass: 50, LocalizationTypes: []string{"NATURAL"}, NavigationTypes: []string{"AUTONOMOUS"}},
		PhysicalParameters: PhysicalParameters{MinimumSpeed: 0, MaximumSpeed: f.MaxSpeed}}
}
