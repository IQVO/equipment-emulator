package robot

import (
	"fmt"
	"testing"
)

func TestNewOrderClearsInstantActionStates(t *testing.T) {
	r := New(cfg())
	if err := r.InstantAction(Action{ID: "ia1", Type: "stateRequest"}); err != nil {
		t.Fatal(err)
	}
	if n := len(r.Snapshot().InstantActionStates); n != 1 {
		t.Fatalf("instant states before order = %d", n)
	}
	if err := r.AcceptOrder(straight()); err != nil {
		t.Fatal(err)
	}
	if s := r.Snapshot(); len(s.InstantActionStates) != 0 {
		t.Fatalf("a new order must clear instantActionStates: %+v", s.InstantActionStates)
	}
}

func TestInstantActionStatesKeepTheMostRecent50(t *testing.T) {
	r := New(cfg())
	for i := 0; i < 60; i++ {
		_ = r.InstantAction(Action{ID: fmt.Sprintf("ia%02d", i), Type: "stateRequest"})
	}
	s := r.Snapshot()
	if len(s.InstantActionStates) != MaxReported {
		t.Fatalf("instant states = %d, want %d", len(s.InstantActionStates), MaxReported)
	}
	if s.InstantActionStates[0].ID != "ia10" || s.InstantActionStates[MaxReported-1].ID != "ia59" {
		t.Fatalf("oldest kept %s, newest %s", s.InstantActionStates[0].ID, s.InstantActionStates[MaxReported-1].ID)
	}
	// re-sending a known id updates it in place and does not evict anything
	_ = r.InstantAction(Action{ID: "ia10", Type: "bogus"})
	s = r.Snapshot()
	if len(s.InstantActionStates) != MaxReported || s.InstantActionStates[0].ID != "ia10" || s.InstantActionStates[0].Status != ActionFailed {
		t.Fatalf("update in place failed: %+v", s.InstantActionStates[0])
	}
}

func TestErrorsKeepTheMostRecent50(t *testing.T) {
	r := New(cfg())
	for i := 0; i < 60; i++ {
		_ = r.InstantAction(Action{ID: fmt.Sprintf("bad%02d", i), Type: "cancelOrder"}) // NO_ORDER_TO_CANCEL
	}
	o := straight()
	o.Nodes[0].Pos.X = 5
	_ = r.AcceptOrder(o) // INVALID_ORDER: the newest error
	s := r.Snapshot()
	if len(s.Errors) != MaxReported {
		t.Fatalf("errors = %d, want %d", len(s.Errors), MaxReported)
	}
	if s.Errors[MaxReported-1].Type != ErrInvalidOrder || s.Errors[0].Type != ErrNoOrderToCancel {
		t.Fatalf("errors not the most recent: first %s last %s", s.Errors[0].Type, s.Errors[MaxReported-1].Type)
	}
}
