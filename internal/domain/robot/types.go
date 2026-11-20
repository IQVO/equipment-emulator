// Package robot is the simulation model of one autonomous mobile robot as
// VDA 5050 v3 describes the robot side: it executes released nodes and
// edges, runs node actions, drains its battery and reports its state. It
// knows nothing about MQTT or JSON.
package robot

import "time"

// Position is a pose on a map in the project-specific coordinate system (metres, radians).
type Position struct {
	X, Y, Theta float64
	MapID       string
}

// Action is a VDA 5050 action. Supported types: pick, drop, startCharging,
// stopCharging (node actions) and cancelOrder, startCharging, stopCharging,
// stateRequest, factsheetRequest (instant actions).
type Action struct {
	ID     string
	Type   string
	Params map[string]string
}

// Node is one node of an order.
type Node struct {
	ID       string
	Seq      uint32
	Released bool
	Pos      Position
	Actions  []Action
}

// Edge joins Nodes[i] to Nodes[i+1] of an order.
type Edge struct {
	ID          string
	Seq         uint32
	Released    bool
	StartNodeID string
	EndNodeID   string
	MaxSpeed    float64 // m/s; 0 = no limit
}

// Order is a VDA 5050 order (or order update).
type Order struct {
	ID       string
	UpdateID uint32
	Nodes    []Node
	Edges    []Edge
}

// ActionStatus is the VDA 5050 actionStatus.
type ActionStatus string

const (
	ActionWaiting  ActionStatus = "WAITING"
	ActionRunning  ActionStatus = "RUNNING"
	ActionFinished ActionStatus = "FINISHED"
	ActionFailed   ActionStatus = "FAILED"
)

// ErrorLevel is the VDA 5050 v3 errorLevel.
type ErrorLevel string

const (
	LevelWarning  ErrorLevel = "WARNING"
	LevelUrgent   ErrorLevel = "URGENT"
	LevelCritical ErrorLevel = "CRITICAL"
	LevelFatal    ErrorLevel = "FATAL"
)

// VDA 5050 errorType values used by the emulator. The enumeration is
// extensible in the standard; the last three are emulator extensions.
const (
	ErrValidation           = "VALIDATION_FAILURE"
	ErrInvalidOrder         = "INVALID_ORDER"
	ErrOutdatedUpdate       = "OUTDATED_ORDER_UPDATE"
	ErrSameUpdateID         = "SAME_ORDER_UPDATE_ID"
	ErrNoOrderToCancel      = "NO_ORDER_TO_CANCEL"
	ErrInvalidInstantAction = "INVALID_INSTANT_ACTION"
	ErrLoadNotAvailable     = "LOAD_NOT_AVAILABLE"
	ErrRobotBlocked         = "ROBOT_BLOCKED"
	ErrBatteryEmpty         = "BATTERY_EMPTY"
)

// Error is one active robot error.
type Error struct {
	Type        string
	Level       ErrorLevel
	Description string
}

// RejectError is returned when an order or instant action is rejected.
type RejectError struct {
	Type   string
	Reason string
}

func (e *RejectError) Error() string { return e.Type + ": " + e.Reason }

// NodeState and EdgeState list what is still to be traversed.
type NodeState struct {
	ID       string
	Seq      uint32
	Released bool
}

// EdgeState is the state of one edge still to be traversed.
type EdgeState struct {
	ID       string
	Seq      uint32
	Released bool
}

// ActionState is the status of one action.
type ActionState struct {
	ID     string
	Type   string
	Status ActionStatus
}

// State is the robot's snapshot, the content of a VDA 5050 state message.
type State struct {
	Manufacturer        string
	SerialNumber        string
	OrderID             string
	OrderUpdateID       uint32
	LastNodeID          string
	LastNodeSeq         uint32
	NodeStates          []NodeState
	EdgeStates          []EdgeState
	Position            Position
	Driving             bool
	ActionStates        []ActionState
	InstantActionStates []ActionState
	StateOfCharge       float64
	Charging            bool
	LoadID              string
	Errors              []Error
}

// Factsheet is the subset of the VDA 5050 factsheet the emulator publishes.
type Factsheet struct {
	Manufacturer     string
	SerialNumber     string
	MaxSpeed         float64
	SupportedActions []string
}

// Config parameterises one simulated robot.
type Config struct {
	Manufacturer      string
	SerialNumber      string
	Start             Position
	Speed             float64       // m/s
	DrainPerMeter     float64       // % state of charge per metre driven
	ChargePerSecond   float64       // % per second while charging
	ActionDuration    time.Duration // time one node action takes
	PositionTolerance float64       // m: how far the first node of a new order may be
}

// Peripherals is the world around the robot: picking or dropping a load at
// a node may need a peripheral (an AS/RS port) to agree.
type Peripherals interface {
	Pick(nodeID, loadID string) error
	Drop(nodeID, loadID string) error
}
