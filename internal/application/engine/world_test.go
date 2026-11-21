package engine

import (
	"strings"
	"testing"
	"time"
)

func TestBuildRejectsInvalidWorlds(t *testing.T) {
	asrsSpec := func(id string, ports ...PortSpec) AsrsSpec {
		return AsrsSpec{ID: id, Aisles: 1, Levels: 1, Slots: 1, Ports: ports}
	}
	cases := map[string]struct {
		w    World
		want string
	}{
		"empty manufacturer": {World{Robots: []RobotSpec{{SerialNumber: "R1", Speed: 1}}}, "manufacturer"},
		"duplicate asrs id":  {World{Asrs: []AsrsSpec{asrsSpec("A"), asrsSpec("A")}}, "asrs id"},
		"empty asrs id":      {World{Asrs: []AsrsSpec{asrsSpec("")}}, "asrs id"},
		"duplicate port node in one asrs": {World{Asrs: []AsrsSpec{asrsSpec("A",
			PortSpec{ID: "IN1", Aisle: 1, Kind: "INPUT", NodeID: "n"}, PortSpec{ID: "OUT1", Aisle: 1, Kind: "OUTPUT", NodeID: "n"})}}, "nodeId"},
		"duplicate port node across asrs": {World{Asrs: []AsrsSpec{
			asrsSpec("A", PortSpec{ID: "IN1", Aisle: 1, Kind: "INPUT", NodeID: "n"}),
			asrsSpec("B", PortSpec{ID: "IN1", Aisle: 1, Kind: "INPUT", NodeID: "n"})}}, "nodeId"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, _, _, err := c.w.Build(time.Now())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, c.want)
			}
		})
	}
}

func TestBuildAcceptsDistinctIDsAndNodes(t *testing.T) {
	w := World{
		Robots: []RobotSpec{{Manufacturer: "EMU", SerialNumber: "R1", Speed: 1}},
		Asrs: []AsrsSpec{
			{ID: "A", Aisles: 1, Levels: 1, Slots: 1, Ports: []PortSpec{{ID: "IN1", Aisle: 1, Kind: "INPUT", NodeID: "a"}, {ID: "OUT1", Aisle: 1, Kind: "OUTPUT"}}},
			{ID: "B", Aisles: 1, Levels: 1, Slots: 1, Ports: []PortSpec{{ID: "IN1", Aisle: 1, Kind: "INPUT", NodeID: "b"}, {ID: "OUT1", Aisle: 1, Kind: "OUTPUT"}}},
		},
	}
	_, robots, systems, nodes, err := w.Build(time.Now())
	if err != nil || len(robots) != 1 || len(systems) != 2 || len(nodes) != 2 {
		t.Fatalf("err=%v robots=%d systems=%d nodes=%d", err, len(robots), len(systems), len(nodes))
	}
}
