package engine

import (
	"fmt"
	"time"

	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
	"github.com/IQVO/equipment-emulator/internal/domain/simclock"
)

// World describes everything the emulator simulates (loaded from YAML by internal/config).
type World struct {
	Robots []RobotSpec `yaml:"robots"`
	Asrs   []AsrsSpec  `yaml:"asrs"`
}

type RobotSpec struct {
	Manufacturer    string  `yaml:"manufacturer"`
	SerialNumber    string  `yaml:"serialNumber"`
	StartX          float64 `yaml:"startX"`
	StartY          float64 `yaml:"startY"`
	MapID           string  `yaml:"mapId"`
	Speed           float64 `yaml:"speed"`
	DrainPerMeter   float64 `yaml:"drainPerMeter"`
	ChargePerSecond float64 `yaml:"chargePerSecond"`
	ActionSeconds   float64 `yaml:"actionSeconds"`
}

type PortSpec struct {
	ID     string `yaml:"id"`
	Aisle  int    `yaml:"aisle"`
	Kind   string `yaml:"kind"`   // INPUT | OUTPUT
	NodeID string `yaml:"nodeId"` // the VDA 5050 node where robots pick or drop at this port
}

type AsrsSpec struct {
	ID              string     `yaml:"id"`
	Aisles          int        `yaml:"aisles"`
	Levels          int        `yaml:"levels"`
	Slots           int        `yaml:"slots"`
	DoubleDeep      bool       `yaml:"doubleDeep"`
	BaseSeconds     float64    `yaml:"baseSeconds"`
	PerLevelSeconds float64    `yaml:"perLevelSeconds"`
	PerSlotSeconds  float64    `yaml:"perSlotSeconds"`
	Ports           []PortSpec `yaml:"ports"`
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// Build creates the simulation objects described by the world.
func (w World) Build(start time.Time) (*simclock.Sim, []*robot.Robot, []*asrs.System, map[string]PortNode, error) {
	var robots []*robot.Robot
	seen := map[string]bool{}
	for _, r := range w.Robots {
		if r.SerialNumber == "" || seen[r.SerialNumber] {
			return nil, nil, nil, nil, fmt.Errorf("robot serial %q is empty or duplicated", r.SerialNumber)
		}
		if r.Speed <= 0 {
			return nil, nil, nil, nil, fmt.Errorf("robot %q speed must be positive", r.SerialNumber)
		}
		seen[r.SerialNumber] = true
		robots = append(robots, robot.New(robot.Config{
			Manufacturer: r.Manufacturer, SerialNumber: r.SerialNumber,
			Start: robot.Position{X: r.StartX, Y: r.StartY, MapID: r.MapID},
			Speed: r.Speed, DrainPerMeter: r.DrainPerMeter, ChargePerSecond: r.ChargePerSecond,
			ActionDuration: secs(r.ActionSeconds), PositionTolerance: 0.5,
		}))
	}
	var systems []*asrs.System
	nodes := map[string]PortNode{}
	for _, a := range w.Asrs {
		var ports []asrs.Port
		for _, p := range a.Ports {
			ports = append(ports, asrs.Port{ID: p.ID, Aisle: p.Aisle, Kind: asrs.PortKind(p.Kind)})
			if p.NodeID != "" {
				nodes[p.NodeID] = PortNode{AsrsID: a.ID, PortID: p.ID}
			}
		}
		s, err := asrs.New(a.ID, asrs.Layout{Aisles: a.Aisles, Levels: a.Levels, Slots: a.Slots, DoubleDeep: a.DoubleDeep, Ports: ports},
			asrs.Timing{Base: secs(a.BaseSeconds), PerLevel: secs(a.PerLevelSeconds), PerSlot: secs(a.PerSlotSeconds)})
		if err != nil {
			return nil, nil, nil, nil, err
		}
		systems = append(systems, s)
	}
	return simclock.NewSim(start), robots, systems, nodes, nil
}
