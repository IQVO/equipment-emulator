// Package telegram is the AS/RS PLC telegram protocol v1 (docs/telegram-protocol.md).
package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Type string

const (
	Store        Type = "STORE"
	Retrieve     Type = "RETRIEVE"
	Move         Type = "MOVE"
	Cancel       Type = "CANCEL"
	StateRequest Type = "STATE_REQUEST"
	Ack          Type = "ACK"
	JobDone      Type = "JOB_DONE"
	JobFailed    Type = "JOB_FAILED"
	UnitState    Type = "UNIT_STATE"
	PortArrival  Type = "PORT_ARRIVAL"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

type Position struct {
	Aisle int `json:"aisle"`
	Level int `json:"level"`
	Slot  int `json:"slot"`
	Depth int `json:"depth"`
}

type Telegram struct {
	ID          string    `json:"telegramId"`
	Type        Type      `json:"type"`
	Timestamp   time.Time `json:"-"`
	JobID       string    `json:"jobId,omitempty"`
	ContainerID string    `json:"containerId,omitempty"`
	PortID      string    `json:"portId,omitempty"`
	From        *Position `json:"from,omitempty"`
	To          *Position `json:"to,omitempty"`
	AckOf       string    `json:"ackTelegramId,omitempty"`
	Accepted    *bool     `json:"accepted,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	UnitID      string    `json:"unitId,omitempty"`
	State       string    `json:"state,omitempty"`
}

type wire struct {
	Telegram
	TS string `json:"timestamp"`
}

func DownTopic(asrsID string) string { return "asrs/v1/" + asrsID + "/down" }
func UpTopic(asrsID string) string   { return "asrs/v1/" + asrsID + "/up" }

// Encode validates and marshals a telegram.
func Encode(t Telegram) ([]byte, error) {
	if err := validate(t); err != nil {
		return nil, err
	}
	return json.Marshal(wire{Telegram: t, TS: t.Timestamp.UTC().Format(timeLayout)})
}

// Decode unmarshals and validates a telegram.
func Decode(b []byte) (Telegram, error) {
	var w wire
	// Keep the partially decoded telegram on every error but a JSON syntax
	// error, so the gateway can NACK it by ID.
	if err := json.Unmarshal(b, &w); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return Telegram{}, fmt.Errorf("MALFORMED: %w", err)
		}
		return w.Telegram, fmt.Errorf("MALFORMED: %w", err)
	}
	t := w.Telegram
	ts, err := time.Parse(time.RFC3339Nano, w.TS)
	if err != nil {
		return t, fmt.Errorf("MALFORMED: timestamp: %w", err)
	}
	t.Timestamp = ts
	return t, validate(t)
}

func validate(t Telegram) error {
	if t.ID == "" {
		return errors.New("MALFORMED: telegramId is required")
	}
	need := func(ok bool, field string) error {
		if !ok {
			return fmt.Errorf("MALFORMED: %s requires %s", t.Type, field)
		}
		return nil
	}
	switch t.Type {
	case Store:
		return errors.Join(need(t.JobID != "", "jobId"), need(t.ContainerID != "", "containerId"), need(t.PortID != "", "portId"), need(t.To != nil, "to"))
	case Retrieve:
		return errors.Join(need(t.JobID != "", "jobId"), need(t.ContainerID != "", "containerId"), need(t.PortID != "", "portId"), need(t.From != nil, "from"))
	case Move:
		return errors.Join(need(t.JobID != "", "jobId"), need(t.ContainerID != "", "containerId"), need(t.From != nil, "from"), need(t.To != nil, "to"))
	case Cancel, JobDone:
		return need(t.JobID != "", "jobId")
	case JobFailed:
		return errors.Join(need(t.JobID != "", "jobId"), need(t.Reason != "", "reason"))
	case Ack:
		return errors.Join(need(t.AckOf != "", "ackTelegramId"), need(t.Accepted != nil, "accepted"))
	case UnitState:
		return errors.Join(need(t.UnitID != "", "unitId"), need(t.State != "", "state"))
	case PortArrival:
		return need(t.PortID != "", "portId")
	case StateRequest:
		return nil
	default:
		return fmt.Errorf("MALFORMED: unknown type %q", t.Type)
	}
}

// NewAck builds the ACK for a received telegram. A non-nil err refuses it,
// with err's message as the reason.
func NewAck(of Telegram, err error, id string, at time.Time) Telegram {
	accepted := err == nil
	a := Telegram{ID: id, Type: Ack, Timestamp: at, AckOf: of.ID, Accepted: &accepted}
	if err != nil {
		a.Reason = reasonCode(err.Error())
	}
	return a
}

// reasonCode reduces an error message to its bare code: the text before the
// first newline or ':', trimmed. Details are logged by the sender, not sent.
func reasonCode(msg string) string {
	msg, _, _ = strings.Cut(msg, "\n")
	msg, _, _ = strings.Cut(msg, ":")
	return strings.TrimSpace(msg)
}
