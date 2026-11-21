// Package vda5050 is the VDA 5050 v3.0.0 wire format (JSON over MQTT), the
// subset docs/vda5050-subset.md lists.
package vda5050

import "fmt"

const (
	InterfaceName = "vda5050"
	MajorVersion  = "v3"
	Version       = "3.0.0"
	TimeLayout    = "2006-01-02T15:04:05.000Z"

	ConnectionOnline  = "ONLINE"
	ConnectionOffline = "OFFLINE"
	ConnectionBroken  = "CONNECTION_BROKEN"
)

// Topic is interfaceName/majorVersion/manufacturer/serialNumber/topic.
func Topic(manufacturer, serial, name string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", InterfaceName, MajorVersion, manufacturer, serial, name)
}

type Header struct {
	HeaderID     uint32 `json:"headerId"`
	Timestamp    string `json:"timestamp"`
	Version      string `json:"version"`
	Manufacturer string `json:"manufacturer"`
	SerialNumber string `json:"serialNumber"`
}

type NodePosition struct {
	X     float64  `json:"x"`
	Y     float64  `json:"y"`
	Theta *float64 `json:"theta,omitempty"`
	MapID string   `json:"mapId"`
}

type ActionParameter struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type Action struct {
	ActionType       string            `json:"actionType"`
	ActionID         string            `json:"actionId"`
	BlockingType     string            `json:"blockingType"`
	ActionParameters []ActionParameter `json:"actionParameters,omitempty"`
}

type Node struct {
	NodeID       string        `json:"nodeId"`
	SequenceID   uint32        `json:"sequenceId"`
	Released     bool          `json:"released"`
	NodePosition *NodePosition `json:"nodePosition,omitempty"`
	Actions      []Action      `json:"actions"`
}

type Edge struct {
	EdgeID      string   `json:"edgeId"`
	SequenceID  uint32   `json:"sequenceId"`
	Released    bool     `json:"released"`
	StartNodeID string   `json:"startNodeId"`
	EndNodeID   string   `json:"endNodeId"`
	MaxSpeed    *float64 `json:"maxSpeed,omitempty"`
	Actions     []Action `json:"actions"`
}

type Order struct {
	Header
	OrderID       string `json:"orderId"`
	OrderUpdateID uint32 `json:"orderUpdateId"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

type InstantActions struct {
	Header
	Actions []Action `json:"actions"`
}

type NodeState struct {
	NodeID     string `json:"nodeId"`
	SequenceID uint32 `json:"sequenceId"`
	Released   bool   `json:"released"`
}

type EdgeState struct {
	EdgeID     string `json:"edgeId"`
	SequenceID uint32 `json:"sequenceId"`
	Released   bool   `json:"released"`
}

type MobileRobotPosition struct {
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Theta     float64 `json:"theta"`
	MapID     string  `json:"mapId"`
	Localized bool    `json:"localized"`
}

type ActionState struct {
	ActionID     string `json:"actionId"`
	ActionType   string `json:"actionType,omitempty"`
	ActionStatus string `json:"actionStatus"`
}

type PowerSupply struct {
	StateOfCharge float64 `json:"stateOfCharge"`
	Charging      bool    `json:"charging"`
}

type Error struct {
	ErrorType        string `json:"errorType"`
	ErrorLevel       string `json:"errorLevel"`
	ErrorDescription string `json:"errorDescription,omitempty"`
}

type SafetyState struct {
	ActiveEmergencyStop string `json:"activeEmergencyStop"`
	FieldViolation      bool   `json:"fieldViolation"`
}

type Load struct {
	LoadID string `json:"loadId"`
}

type State struct {
	Header
	OrderID             string               `json:"orderId"`
	OrderUpdateID       uint32               `json:"orderUpdateId"`
	LastNodeID          string               `json:"lastNodeId"`
	LastNodeSequenceID  uint32               `json:"lastNodeSequenceId"`
	NodeStates          []NodeState          `json:"nodeStates"`
	EdgeStates          []EdgeState          `json:"edgeStates"`
	MobileRobotPosition *MobileRobotPosition `json:"mobileRobotPosition,omitempty"`
	Driving             bool                 `json:"driving"`
	Paused              bool                 `json:"paused"`
	ActionStates        []ActionState        `json:"actionStates"`
	InstantActionStates []ActionState        `json:"instantActionStates"`
	PowerSupply         PowerSupply          `json:"powerSupply"`
	OperatingMode       string               `json:"operatingMode"`
	Errors              []Error              `json:"errors"`
	Information         []any                `json:"information"`
	SafetyState         SafetyState          `json:"safetyState"`
	Loads               []Load               `json:"loads,omitempty"`
}

type Connection struct {
	Header
	ConnectionState string `json:"connectionState"`
}

type TypeSpecification struct {
	SeriesName        string   `json:"seriesName"`
	MaximumLoadMass   float64  `json:"maximumLoadMass"`
	LocalizationTypes []string `json:"localizationTypes"`
	NavigationTypes   []string `json:"navigationTypes"`
}

type PhysicalParameters struct {
	MinimumSpeed float64 `json:"minimumSpeed"`
	MaximumSpeed float64 `json:"maximumSpeed"`
}

// Factsheet is the subset the emulator publishes (typeSpecification and physicalParameters).
type Factsheet struct {
	Header
	TypeSpecification  TypeSpecification  `json:"typeSpecification"`
	PhysicalParameters PhysicalParameters `json:"physicalParameters"`
}
