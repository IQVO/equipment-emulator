package vda5050

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

const orderJSON = `{"headerId":1,"timestamp":"2026-11-19T08:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1",
 "orderId":"o1","orderUpdateId":0,
 "nodes":[{"nodeId":"n1","sequenceId":0,"released":true,"nodePosition":{"x":0,"y":0,"mapId":"S1"},
           "actions":[{"actionType":"pick","actionId":"a1","blockingType":"HARD","actionParameters":[{"key":"loadId","value":"C1"}]}]},
          {"nodeId":"n2","sequenceId":2,"released":true,"nodePosition":{"x":10,"y":0,"mapId":"S1"},"actions":[]}],
 "edges":[{"edgeId":"e1","sequenceId":1,"released":true,"startNodeId":"n1","endNodeId":"n2","maxSpeed":0.8,"actions":[]}]}`

func TestToOrder(t *testing.T) {
	var o Order
	if err := json.Unmarshal([]byte(orderJSON), &o); err != nil {
		t.Fatal(err)
	}
	d, err := ToOrder(o)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "o1" || len(d.Nodes) != 2 || d.Nodes[1].Pos.X != 10 || d.Edges[0].MaxSpeed != 0.8 {
		t.Fatalf("order = %+v", d)
	}
	if a := d.Nodes[0].Actions[0]; a.Type != "pick" || a.Params["loadId"] != "C1" {
		t.Fatalf("action = %+v", a)
	}
}

func TestToOrderRejectsEdgeActionsAndMissingPositions(t *testing.T) {
	var o Order
	_ = json.Unmarshal([]byte(orderJSON), &o)
	o.Edges[0].Actions = []Action{{ActionType: "beep", ActionID: "x"}}
	if _, err := ToOrder(o); err == nil {
		t.Fatal("edge actions are not supported")
	}
	_ = json.Unmarshal([]byte(orderJSON), &o)
	o.Nodes[1].NodePosition = nil
	if _, err := ToOrder(o); err == nil {
		t.Fatal("node positions are required by the emulator")
	}
}

func TestFromStateUsesV3FieldNames(t *testing.T) {
	s := robot.State{Manufacturer: "EMU", SerialNumber: "R1", OrderID: "o1", LastNodeID: "n1",
		Position: robot.Position{X: 1, Y: 2, MapID: "S1"}, StateOfCharge: 88, LoadID: "C1",
		ActionStates: []robot.ActionState{{ID: "a1", Type: "pick", Status: robot.ActionFinished}},
		Errors:       []robot.Error{{Type: robot.ErrRobotBlocked, Level: robot.LevelCritical}}}
	h := NewHeader(7, time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC), "EMU", "R1")
	b, _ := json.Marshal(FromState(s, h))
	for _, want := range []string{`"mobileRobotPosition":{"x":1,"y":2,"theta":0,"mapId":"S1","localized":true}`,
		`"powerSupply":{"stateOfCharge":88,"charging":false}`, `"instantActionStates":[]`,
		`"errors":[{"errorType":"ROBOT_BLOCKED","errorLevel":"CRITICAL"}]`, `"loads":[{"loadId":"C1"}]`,
		`"operatingMode":"AUTOMATIC"`, `"timestamp":"2026-11-19T08:00:00.000Z"`, `"version":"3.0.0"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("state JSON lacks %s\n%s", want, b)
		}
	}
}

func TestTopic(t *testing.T) {
	if got := Topic("EMU", "R1", "order"); got != "vda5050/v3/EMU/R1/order" {
		t.Fatal(got)
	}
}
