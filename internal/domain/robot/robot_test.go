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

func TestEmptyBatteryNeverOvershootsWithInexactRatio(t *testing.T) {
	c := cfg()
	c.DrainPerMeter = 0.11
	r := New(c)
	r.SetStateOfCharge(0.3) // 0.3/0.11 m of range, not exactly representable
	_ = r.AcceptOrder(straight())
	r.Tick(60*time.Second, &fakePeripherals{})
	s := r.Snapshot()
	if s.LastNodeID != "n1" || s.Driving || s.StateOfCharge != 0 || math.Abs(s.Position.X-0.3/0.11) > 1e-9 {
		t.Fatalf("state = %+v", s)
	}
	if len(s.Errors) != 1 || s.Errors[0].Type != ErrBatteryEmpty {
		t.Fatalf("errors = %+v", s.Errors)
	}
}

// path builds a fully released order through the given points; the first
// point must be the robot's start.
func path(maxSpeeds []float64, pts ...Position) Order {
	o := Order{ID: "o1"}
	for i, p := range pts {
		p.MapID = "S1"
		o.Nodes = append(o.Nodes, Node{ID: "n" + string(rune('1'+i)), Seq: uint32(2 * i), Released: true, Pos: p})
		if i > 0 {
			e := edge("e"+string(rune('0'+i)), uint32(2*i-1), o.Nodes[i-1].ID, o.Nodes[i].ID, true)
			if len(maxSpeeds) >= i {
				e.MaxSpeed = maxSpeeds[i-1]
			}
			o.Edges = append(o.Edges, e)
		}
	}
	return o
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDiagonalEdgeInterpolatesAndHeads(t *testing.T) {
	r := New(cfg())
	if err := r.AcceptOrder(path(nil, Position{}, Position{X: 3, Y: 4})); err != nil {
		t.Fatal(err)
	}
	r.Tick(2*time.Second, &fakePeripherals{}) // 2 of 5 m
	s := r.Snapshot()
	if !near(s.Position.X, 1.2) || !near(s.Position.Y, 1.6) || !near(s.Position.Theta, math.Atan2(4, 3)) {
		t.Fatalf("mid-edge position = %+v", s.Position)
	}
	r.Tick(1*time.Second, &fakePeripherals{}) // 3 of 5 m: moving from a non-origin point
	s = r.Snapshot()
	if !near(s.Position.X, 1.8) || !near(s.Position.Y, 2.4) || !near(s.Position.Theta, math.Atan2(4, 3)) {
		t.Fatalf("second tick position = %+v", s.Position)
	}
	r.Tick(2*time.Second, &fakePeripherals{})
	s = r.Snapshot()
	if !near(s.Position.X, 3) || !near(s.Position.Y, 4) || s.LastNodeID != "n2" || s.Driving {
		t.Fatalf("at arrival = %+v", s)
	}
	if !near(s.StateOfCharge, 100-5*0.1) {
		t.Fatalf("soc = %v, want drain over the Euclidean 5 m", s.StateOfCharge)
	}
}

func TestNegativeDirectionMove(t *testing.T) {
	c := cfg()
	c.Start = Position{X: 10}
	r := New(c)
	if err := r.AcceptOrder(path(nil, Position{X: 10}, Position{X: 0})); err != nil {
		t.Fatal(err)
	}
	r.Tick(3*time.Second, &fakePeripherals{})
	s := r.Snapshot()
	if !near(s.Position.X, 7) || !near(s.Position.Y, 0) || !near(math.Abs(s.Position.Theta), math.Pi) {
		t.Fatalf("position = %+v", s.Position)
	}
	r.Tick(2*time.Second, &fakePeripherals{})
	if x := r.Snapshot().Position.X; !near(x, 5) {
		t.Fatalf("x = %v, want 5", x)
	}
}

func TestNegativeYMove(t *testing.T) {
	c := cfg()
	c.Start = Position{Y: 4}
	r := New(c)
	_ = r.AcceptOrder(path(nil, Position{Y: 4}, Position{X: 3, Y: 0}))
	r.Tick(2*time.Second, &fakePeripherals{})
	s := r.Snapshot()
	if !near(s.Position.X, 1.2) || !near(s.Position.Y, 4-1.6) || !near(s.Position.Theta, math.Atan2(-4, 3)) {
		t.Fatalf("position = %+v", s.Position)
	}
}

func TestEdgeMaxSpeed(t *testing.T) {
	cases := []struct {
		name string
		max  float64
		want float64
	}{
		{"slower than robot", 0.5, 0.5},
		{"faster than robot", 3, 1},
		{"unlimited", 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(cfg())
			_ = r.AcceptOrder(path([]float64{tc.max}, Position{}, Position{X: 10}))
			r.Tick(time.Second, &fakePeripherals{})
			if x := r.Snapshot().Position.X; !near(x, tc.want) {
				t.Fatalf("x = %v, want %v", x, tc.want)
			}
		})
	}
}

func TestMaxSpeedTimeAccountingOnArrival(t *testing.T) {
	// 1 m at 0.5 m/s takes 2 s, leaving 1 s at robot speed on the next edge.
	r := New(cfg())
	_ = r.AcceptOrder(path([]float64{0.5, 0}, Position{}, Position{X: 1}, Position{X: 11}))
	r.Tick(3*time.Second, &fakePeripherals{})
	s := r.Snapshot()
	if s.LastNodeID != "n2" || !near(s.Position.X, 2) {
		t.Fatalf("state = %+v", s)
	}
}

func TestLeftoverTimeContinuesOntoNextEdge(t *testing.T) {
	r := New(cfg())
	_ = r.AcceptOrder(path(nil, Position{}, Position{X: 2}, Position{X: 12}))
	r.Tick(5*time.Second, &fakePeripherals{}) // 2 s on e1, 3 s on e2
	s := r.Snapshot()
	if s.LastNodeID != "n2" || !near(s.Position.X, 5) || !s.Driving {
		t.Fatalf("state = %+v", s)
	}
}

func TestBatteryReachBoundary(t *testing.T) {
	cases := []struct {
		name    string
		soc     float64
		arrives bool
		wantX   float64
	}{
		{"exactly enough", 1, true, 10},
		{"slightly short", 0.99, false, 9.9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := cfg()
			c.DrainPerMeter = 0.1
			r := New(c)
			r.SetStateOfCharge(tc.soc)
			_ = r.AcceptOrder(straight())
			r.Tick(30*time.Second, &fakePeripherals{})
			s := r.Snapshot()
			if (s.LastNodeID == "n2") != tc.arrives || !near(s.Position.X, tc.wantX) {
				t.Fatalf("state = %+v", s)
			}
		})
	}
}

func TestDrainDisabledWhenZero(t *testing.T) {
	c := cfg()
	c.DrainPerMeter = 0
	r := New(c)
	r.SetStateOfCharge(1)
	_ = r.AcceptOrder(straight())
	r.Tick(20*time.Second, &fakePeripherals{})
	s := r.Snapshot()
	if s.LastNodeID != "n2" || s.StateOfCharge != 1 {
		t.Fatalf("state = %+v", s)
	}
}

func TestRobotWithEmptyBatteryDoesNotMove(t *testing.T) {
	r := New(cfg())
	_ = r.AcceptOrder(straight())
	r.SetStateOfCharge(0)
	r.Tick(5*time.Second, &fakePeripherals{})
	if s := r.Snapshot(); s.Position.X != 0 || s.Driving {
		t.Fatalf("state = %+v", s)
	}
}

func pickOrder(id string) Order {
	return Order{ID: id, Nodes: []Node{node("n1", 0, 0, true,
		Action{ID: "a1", Type: "pick", Params: map[string]string{"loadId": "L1"}})}}
}

func TestActionTiming(t *testing.T) {
	status := func(r *Robot) ActionStatus { return r.Snapshot().ActionStates[0].Status }
	exact := New(cfg())
	_ = exact.AcceptOrder(pickOrder("o1"))
	exact.Tick(time.Second, &fakePeripherals{})
	if got := status(exact); got != ActionFinished {
		t.Fatalf("exact duration: %s", got)
	}
	short := New(cfg())
	_ = short.AcceptOrder(pickOrder("o1"))
	short.Tick(time.Second-time.Nanosecond, &fakePeripherals{})
	if got := status(short); got != ActionRunning {
		t.Fatalf("one ns short: %s", got)
	}
	short.Tick(time.Nanosecond, &fakePeripherals{})
	if got := status(short); got != ActionFinished {
		t.Fatalf("after completing: %s", got)
	}
}

func TestNewOrderAcceptedOnlyAfterLastActionFinishes(t *testing.T) {
	r := New(cfg())
	p := &fakePeripherals{}
	_ = r.AcceptOrder(pickOrder("o1"))
	r.Tick(time.Second-time.Nanosecond, p)
	next := Order{ID: "o2", Nodes: []Node{node("n1", 0, 0, true)}}
	if got := rejectType(t, r.AcceptOrder(next)); got != ErrInvalidOrder {
		t.Fatalf("type = %s", got)
	}
	r.Tick(time.Nanosecond, p)
	if err := r.AcceptOrder(next); err != nil {
		t.Fatalf("after the last action: %v", err)
	}
}

func TestNewOrderRejectedMidDriveAtLastNode(t *testing.T) {
	r := New(cfg())
	_ = r.AcceptOrder(straight())
	r.Tick(9*time.Second, &fakePeripherals{})
	other := Order{ID: "o2", Nodes: []Node{node("n2", 2, 9, true)}}
	if got := rejectType(t, r.AcceptOrder(other)); got != ErrInvalidOrder {
		t.Fatalf("type = %s", got)
	}
	r.Tick(time.Second, &fakePeripherals{})
	if err := r.AcceptOrder(Order{ID: "o2", Nodes: []Node{node("n2", 2, 10, true)}}); err != nil {
		t.Fatal(err)
	}
}

func TestOutdatedAndRepeatedUpdateRejected(t *testing.T) {
	r := New(cfg())
	o := Order{ID: "o1", UpdateID: 5, Nodes: []Node{node("n1", 0, 0, true), node("n2", 2, 10, false)},
		Edges: []Edge{edge("e1", 1, "n1", "n2", false)}}
	_ = r.AcceptOrder(o)
	older := o
	older.UpdateID = 4
	err := r.AcceptOrder(older)
	if got := rejectType(t, err); got != ErrOutdatedUpdate || err.Error() != ErrOutdatedUpdate+": update 4 is older than 5" {
		t.Fatalf("outdated: %v", err)
	}
	same := o
	if got := rejectType(t, r.AcceptOrder(same)); got != ErrSameUpdateID {
		t.Fatalf("same: %s", got)
	}
}

func TestFirstNodeToleranceBoundary(t *testing.T) {
	at := func(x float64) error {
		r := New(cfg()) // tolerance 0.5
		return r.AcceptOrder(Order{ID: "o1", Nodes: []Node{node("other", 0, x, true)}})
	}
	if err := at(0.5); err != nil {
		t.Fatalf("exactly at tolerance: %v", err)
	}
	if got := rejectType(t, at(0.51)); got != ErrInvalidOrder {
		t.Fatalf("type = %s", got)
	}
}

func TestCancelFailsUnfinishedActionsKeepsFinished(t *testing.T) {
	r := New(cfg())
	o := Order{ID: "o1", Nodes: []Node{node("n1", 0, 0, true,
		Action{ID: "a1", Type: "pick", Params: map[string]string{"loadId": "L1"}},
		Action{ID: "a2", Type: "drop"})}}
	_ = r.AcceptOrder(o)
	r.Tick(time.Second, &fakePeripherals{}) // a1 finished, a2 not started
	if err := r.InstantAction(Action{ID: "c1", Type: "cancelOrder"}); err != nil {
		t.Fatal(err)
	}
	got := map[string]ActionStatus{}
	for _, s := range r.Snapshot().ActionStates {
		got[s.ID] = s.Status
	}
	if got["a1"] != ActionFinished || got["a2"] != ActionFailed {
		t.Fatalf("states = %v", got)
	}
}

func TestUpdateKeepsFinishedActionStates(t *testing.T) {
	r := New(cfg())
	o := Order{ID: "o1", Nodes: []Node{
		node("n1", 0, 0, true, Action{ID: "a1", Type: "pick", Params: map[string]string{"loadId": "L1"}}),
		node("n2", 2, 5, true), node("n3", 4, 10, false, Action{ID: "a3", Type: "drop"})},
		Edges: []Edge{edge("e1", 1, "n1", "n2", true), edge("e2", 3, "n2", "n3", false)}}
	_ = r.AcceptOrder(o)
	r.Tick(10*time.Second, &fakePeripherals{}) // a1 finished, waiting at n2
	upd := Order{ID: "o1", UpdateID: 1, Nodes: []Node{node("n2", 2, 5, true), node("n3", 4, 10, true, Action{ID: "a3", Type: "drop"})},
		Edges: []Edge{edge("e2", 3, "n2", "n3", true)}}
	if err := r.AcceptOrder(upd); err != nil {
		t.Fatal(err)
	}
	got := map[string]ActionStatus{}
	for _, s := range r.Snapshot().ActionStates {
		got[s.ID] = s.Status
	}
	if got["a1"] != ActionFinished || got["a3"] != ActionWaiting {
		t.Fatalf("states = %v", got)
	}
}
