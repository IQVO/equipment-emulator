package robot

import (
	"errors"
	"math"
	"testing"
	"time"
)

type fakePeripherals struct {
	picks, drops []string
	pickErr      error
}

func (f *fakePeripherals) Pick(nodeID, loadID string) error {
	if f.pickErr != nil {
		return f.pickErr
	}
	f.picks = append(f.picks, nodeID+":"+loadID)
	return nil
}

func (f *fakePeripherals) Drop(nodeID, loadID string) error {
	f.drops = append(f.drops, nodeID+":"+loadID)
	return nil
}

func cfg() Config {
	return Config{
		Manufacturer: "EMU", SerialNumber: "R1",
		Start: Position{X: 0, Y: 0, MapID: "S1"},
		Speed: 1, DrainPerMeter: 0.1, ChargePerSecond: 1,
		ActionDuration: time.Second, PositionTolerance: 0.5,
	}
}

func node(id string, seq uint32, x float64, released bool, actions ...Action) Node {
	return Node{ID: id, Seq: seq, Released: released, Pos: Position{X: x, MapID: "S1"}, Actions: actions}
}

func edge(id string, seq uint32, from, to string, released bool) Edge {
	return Edge{ID: id, Seq: seq, Released: released, StartNodeID: from, EndNodeID: to}
}

// straight: n1(0) -> n2(10), both released.
func straight() Order {
	return Order{ID: "o1", Nodes: []Node{node("n1", 0, 0, true), node("n2", 2, 10, true)},
		Edges: []Edge{edge("e1", 1, "n1", "n2", true)}}
}

func rejectType(t *testing.T, err error) string {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want *RejectError, got %v", err)
	}
	return re.Type
}

func TestAcceptOrderTraversesFirstNode(t *testing.T) {
	r := New(cfg())
	if err := r.AcceptOrder(straight()); err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot()
	if s.OrderID != "o1" || s.LastNodeID != "n1" || s.LastNodeSeq != 0 {
		t.Fatalf("state = %+v", s)
	}
	if len(s.NodeStates) != 1 || s.NodeStates[0].ID != "n2" || len(s.EdgeStates) != 1 {
		t.Fatalf("remaining = %+v %+v", s.NodeStates, s.EdgeStates)
	}
}

func TestRejectsOrderNotStartingAtRobot(t *testing.T) {
	r := New(cfg())
	o := straight()
	o.Nodes[0].Pos.X = 5
	if got := rejectType(t, r.AcceptOrder(o)); got != ErrInvalidOrder {
		t.Fatalf("type = %s", got)
	}
	if s := r.Snapshot(); s.OrderID != "" || len(s.Errors) != 1 {
		t.Fatalf("state = %+v", s)
	}
}

func TestRejectsStructurallyInvalidOrder(t *testing.T) {
	r := New(cfg())
	o := straight()
	o.Edges[0].EndNodeID = "nX"
	if got := rejectType(t, r.AcceptOrder(o)); got != ErrValidation {
		t.Fatalf("type = %s", got)
	}
	o = straight()
	o.Nodes[0].Actions = []Action{{ID: "a1", Type: "dance"}}
	if got := rejectType(t, r.AcceptOrder(o)); got != ErrValidation {
		t.Fatalf("type = %s", got)
	}
}

func TestDrivesAlongReleasedBase(t *testing.T) {
	r := New(cfg())
	p := &fakePeripherals{}
	_ = r.AcceptOrder(straight())
	r.Tick(4*time.Second, p)
	s := r.Snapshot()
	if !s.Driving || math.Abs(s.Position.X-4) > 1e-9 || s.LastNodeID != "n1" {
		t.Fatalf("after 4s: %+v", s)
	}
	r.Tick(6*time.Second, p)
	s = r.Snapshot()
	if s.LastNodeID != "n2" || s.LastNodeSeq != 2 || math.Abs(s.Position.X-10) > 1e-9 || len(s.NodeStates) != 0 {
		t.Fatalf("after 10s: %+v", s)
	}
	if math.Abs(s.StateOfCharge-99) > 1e-9 {
		t.Fatalf("soc = %v, want 99 (10 m * 0.1 %%/m)", s.StateOfCharge)
	}
}

func TestStopsAtEndOfBaseUntilUpdate(t *testing.T) {
	r := New(cfg())
	p := &fakePeripherals{}
	o := Order{ID: "o1", Nodes: []Node{node("n1", 0, 0, true), node("n2", 2, 5, true), node("n3", 4, 10, false)},
		Edges: []Edge{edge("e1", 1, "n1", "n2", true), edge("e2", 3, "n2", "n3", false)}}
	_ = r.AcceptOrder(o)
	r.Tick(20*time.Second, p)
	if s := r.Snapshot(); s.LastNodeID != "n2" || s.Driving {
		t.Fatalf("should wait at n2: %+v", s)
	}
	upd := Order{ID: "o1", UpdateID: 1, Nodes: []Node{node("n2", 2, 5, true), node("n3", 4, 10, true)},
		Edges: []Edge{edge("e2", 3, "n2", "n3", true)}}
	if err := r.AcceptOrder(upd); err != nil {
		t.Fatal(err)
	}
	r.Tick(5*time.Second, p)
	if s := r.Snapshot(); s.LastNodeID != "n3" || s.OrderUpdateID != 1 {
		t.Fatalf("after update: %+v", s)
	}
}

func TestOrderUpdateRules(t *testing.T) {
	r := New(cfg())
	o := straight()
	o.UpdateID = 2
	_ = r.AcceptOrder(o)
	old := straight()
	old.UpdateID = 1
	if got := rejectType(t, r.AcceptOrder(old)); got != ErrOutdatedUpdate {
		t.Fatalf("type = %s", got)
	}
	same := straight()
	same.UpdateID = 2
	if got := rejectType(t, r.AcceptOrder(same)); got != ErrSameUpdateID {
		t.Fatalf("type = %s", got)
	}
	notStitched := Order{ID: "o1", UpdateID: 3, Nodes: []Node{node("n1", 0, 0, true)}}
	if got := rejectType(t, r.AcceptOrder(notStitched)); got != ErrValidation {
		t.Fatalf("type = %s", got)
	}
}

func TestNewOrderRejectedWhileActive(t *testing.T) {
	r := New(cfg())
	_ = r.AcceptOrder(straight())
	other := straight()
	other.ID = "o2"
	if got := rejectType(t, r.AcceptOrder(other)); got != ErrInvalidOrder {
		t.Fatalf("type = %s", got)
	}
}

func TestPickAndDropGoThroughPeripherals(t *testing.T) {
	r := New(cfg())
	p := &fakePeripherals{}
	o := straight()
	o.Nodes[0].Actions = []Action{{ID: "a1", Type: "pick", Params: map[string]string{"loadId": "C1"}}}
	o.Nodes[1].Actions = []Action{{ID: "a2", Type: "drop"}}
	_ = r.AcceptOrder(o)
	r.Tick(500*time.Millisecond, p)
	if s := r.Snapshot(); s.Driving || s.LoadID != "" || s.ActionStates[0].Status != ActionRunning {
		t.Fatalf("pick should still run: %+v", s)
	}
	r.Tick(20*time.Second, p)
	s := r.Snapshot()
	if len(p.picks) != 1 || p.picks[0] != "n1:C1" || len(p.drops) != 1 || p.drops[0] != "n2:C1" || s.LoadID != "" {
		t.Fatalf("picks=%v drops=%v state=%+v", p.picks, p.drops, s)
	}
	for _, a := range s.ActionStates {
		if a.Status != ActionFinished {
			t.Fatalf("action %s = %s", a.ID, a.Status)
		}
	}
}

func TestFailedPickStopsTheOrder(t *testing.T) {
	r := New(cfg())
	p := &fakePeripherals{pickErr: errors.New("port empty")}
	o := straight()
	o.Nodes[0].Actions = []Action{{ID: "a1", Type: "pick", Params: map[string]string{"loadId": "C1"}}}
	_ = r.AcceptOrder(o)
	r.Tick(10*time.Second, p)
	s := r.Snapshot()
	if s.LastNodeID != "n1" || s.ActionStates[0].Status != ActionFailed || len(s.Errors) != 1 || s.Errors[0].Type != ErrLoadNotAvailable {
		t.Fatalf("state = %+v", s)
	}
}

func TestBlockedRobotDoesNotMove(t *testing.T) {
	r := New(cfg())
	p := &fakePeripherals{}
	_ = r.AcceptOrder(straight())
	r.SetBlocked(true)
	r.Tick(5*time.Second, p)
	s := r.Snapshot()
	if s.Position.X != 0 || s.Driving || len(s.Errors) != 1 || s.Errors[0].Type != ErrRobotBlocked {
		t.Fatalf("blocked: %+v", s)
	}
	r.SetBlocked(false)
	r.Tick(10*time.Second, p)
	if s := r.Snapshot(); s.LastNodeID != "n2" || len(s.Errors) != 0 {
		t.Fatalf("unblocked: %+v", s)
	}
}

func TestEmptyBatteryStopsTheRobot(t *testing.T) {
	r := New(cfg())
	r.SetStateOfCharge(0.2) // 2 m of range
	_ = r.AcceptOrder(straight())
	r.Tick(10*time.Second, &fakePeripherals{})
	s := r.Snapshot()
	if math.Abs(s.Position.X-2) > 1e-9 || s.StateOfCharge != 0 || s.Errors[0].Type != ErrBatteryEmpty {
		t.Fatalf("state = %+v", s)
	}
}

func TestInstantActions(t *testing.T) {
	r := New(cfg())
	if got := rejectType(t, r.InstantAction(Action{ID: "i1", Type: "cancelOrder"})); got != ErrNoOrderToCancel {
		t.Fatalf("type = %s", got)
	}
	_ = r.AcceptOrder(straight())
	r.Tick(3*time.Second, &fakePeripherals{})
	if err := r.InstantAction(Action{ID: "i2", Type: "cancelOrder"}); err != nil {
		t.Fatal(err)
	}
	r.Tick(5*time.Second, &fakePeripherals{})
	s := r.Snapshot()
	if math.Abs(s.Position.X-3) > 1e-9 || s.Driving || s.OrderID != "o1" || len(s.NodeStates) != 0 {
		t.Fatalf("after cancel: %+v", s)
	}
	// a new order may start where the robot stopped
	o := Order{ID: "o2", Nodes: []Node{node("p", 0, 3, true)}}
	if err := r.AcceptOrder(o); err != nil {
		t.Fatal(err)
	}
	if got := rejectType(t, r.InstantAction(Action{ID: "i3", Type: "fly"})); got != ErrInvalidInstantAction {
		t.Fatalf("type = %s", got)
	}
}

func TestChargingRaisesStateOfCharge(t *testing.T) {
	r := New(cfg())
	r.SetStateOfCharge(50)
	_ = r.InstantAction(Action{ID: "c1", Type: "startCharging"})
	r.Tick(10*time.Second, &fakePeripherals{})
	if s := r.Snapshot(); !s.Charging || math.Abs(s.StateOfCharge-60) > 1e-9 {
		t.Fatalf("state = %+v", s)
	}
	_ = r.InstantAction(Action{ID: "c2", Type: "stopCharging"})
	if s := r.Snapshot(); s.Charging {
		t.Fatalf("still charging")
	}
}
