// Package asrs is the simulation model of an automated storage and retrieval
// system as seen from its PLC: one combined shuttle-and-lift unit per aisle
// executes store, retrieve and move jobs one at a time, and output ports
// present retrieved containers.
package asrs

import (
	"fmt"
	"time"
)

type Position struct{ Aisle, Level, Slot, Depth int }

func (p Position) String() string {
	return fmt.Sprintf("A%d-L%d-S%d-D%d", p.Aisle, p.Level, p.Slot, p.Depth)
}

// front is the position in front of a back (depth 2) position.
func (p Position) front() Position { p.Depth = 1; return p }

type PortKind string

const (
	PortInput  PortKind = "INPUT"
	PortOutput PortKind = "OUTPUT"
)

type Port struct {
	ID    string
	Aisle int
	Kind  PortKind
}

type Layout struct {
	Aisles, Levels, Slots int
	DoubleDeep            bool
	Ports                 []Port
}

// Timing: a job takes Base + PerLevel*level + PerSlot*slot.
type Timing struct{ Base, PerLevel, PerSlot time.Duration }

type JobKind string

const (
	KindStore    JobKind = "STORE"
	KindRetrieve JobKind = "RETRIEVE"
	KindMove     JobKind = "MOVE"
)

// Job: STORE uses Port and To, RETRIEVE uses From and Port, MOVE uses From and To.
type Job struct {
	ID          string
	Kind        JobKind
	ContainerID string
	Port        string
	From, To    Position
}

type UnitState string

const (
	UnitIdle    UnitState = "IDLE"
	UnitExecute UnitState = "EXECUTE"
	UnitHeld    UnitState = "HELD"
)

type EventKind string

const (
	EventJobDone     EventKind = "JOB_DONE"
	EventJobFailed   EventKind = "JOB_FAILED"
	EventUnitState   EventKind = "UNIT_STATE"
	EventPortArrival EventKind = "PORT_ARRIVAL"
)

// Event is something the PLC reports upward. A PortArrival with an empty
// ContainerID is a no-read.
type Event struct {
	Kind        EventKind
	JobID       string
	ContainerID string
	PortID      string
	UnitID      string
	Reason      string
	State       UnitState
}

const (
	ReasonUnknownPort            = "UNKNOWN_PORT"
	ReasonWrongPortKind          = "WRONG_PORT_KIND"
	ReasonInvalidPosition        = "INVALID_POSITION"
	ReasonDuplicateJob           = "DUPLICATE_JOB"
	ReasonAisleMismatch          = "AISLE_MISMATCH"
	ReasonUnknownJob             = "UNKNOWN_JOB"
	ReasonJobRunning             = "JOB_RUNNING"
	ReasonPortOccupied           = "PORT_OCCUPIED"
	ReasonPortEmpty              = "PORT_EMPTY"
	ReasonWrongContainer         = "WRONG_CONTAINER"
	ReasonPositionOccupied       = "POSITION_OCCUPIED"
	ReasonPositionBlocked        = "POSITION_BLOCKED"
	ReasonContainerNotAtPort     = "CONTAINER_NOT_AT_PORT"
	ReasonContainerNotAtPosition = "CONTAINER_NOT_AT_POSITION"
	ReasonCancelled              = "CANCELLED"
)

// RejectError is a command the PLC refuses at once (it goes back in the ACK).
type RejectError struct{ Reason string }

func (e *RejectError) Error() string { return e.Reason }

// UnitView is a read-only view of one aisle's unit.
type UnitView struct {
	ID         string
	State      UnitState
	CurrentJob string
	Queued     int
}

// View is a read-only view of the whole AS/RS (the admin adapter maps it to JSON).
type View struct {
	ID    string
	Slots map[string]string // position -> container
	Ports map[string]string // port -> container ("" = empty)
	Units []UnitView
}
