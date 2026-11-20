// Package http is the emulator's admin API: inspection, fault injection and seeding.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

type Admin interface {
	Robots() []robot.State
	Asrs(id string) (asrs.View, bool)
	InjectFault(ctx context.Context, f engine.Fault) error
	Seed(asrsID string, p asrs.Position, containerID string) error
	PlaceAtPort(asrsID, portID, containerID string) error
}

type positionJSON struct {
	Aisle int `json:"aisle"`
	Level int `json:"level"`
	Slot  int `json:"slot"`
	Depth int `json:"depth"`
}

type robotJSON struct {
	SerialNumber  string   `json:"serialNumber"`
	OrderID       string   `json:"orderId"`
	LastNodeID    string   `json:"lastNodeId"`
	X             float64  `json:"x"`
	Y             float64  `json:"y"`
	Driving       bool     `json:"driving"`
	StateOfCharge float64  `json:"stateOfCharge"`
	Charging      bool     `json:"charging"`
	LoadID        string   `json:"loadId,omitempty"`
	Errors        []string `json:"errors"`
}

type unitJSON struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	CurrentJob string `json:"currentJob"`
	Queued     int    `json:"queued"`
}

type asrsJSON struct {
	ID    string            `json:"id"`
	Slots map[string]string `json:"slots"`
	Ports map[string]string `json:"ports"`
	Units []unitJSON        `json:"units"`
}

// toAsrsJSON maps the domain view to the AsrsView schema; required maps and
// arrays serialise as {} and [] when empty.
func toAsrsJSON(v asrs.View) asrsJSON {
	out := asrsJSON{ID: v.ID, Slots: map[string]string{}, Ports: map[string]string{}, Units: []unitJSON{}}
	for k, c := range v.Slots {
		out.Slots[k] = c
	}
	for k, c := range v.Ports {
		out.Ports[k] = c
	}
	for _, u := range v.Units {
		out.Units = append(out.Units, unitJSON{ID: u.ID, State: string(u.State), CurrentJob: u.CurrentJob, Queued: u.Queued})
	}
	return out
}

type faultJSON struct {
	Kind   string  `json:"kind"`
	Target string  `json:"target"`
	Aisle  int     `json:"aisle"`
	Port   string  `json:"port"`
	Active bool    `json:"active"`
	Value  float64 `json:"value"`
}

func problem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": title, "status": status, "detail": detail})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func commandError(w http.ResponseWriter, err error) {
	var re *asrs.RejectError
	switch {
	case errors.Is(err, engine.ErrUnknownRobot), errors.Is(err, engine.ErrUnknownAsrs):
		problem(w, http.StatusNotFound, "Not Found", err.Error())
	case errors.As(err, &re):
		problem(w, http.StatusConflict, "Conflict", re.Reason)
	default:
		problem(w, http.StatusUnprocessableEntity, "Unprocessable Entity", err.Error())
	}
}

type handlers struct{ a Admin }

func (h handlers) robots(w http.ResponseWriter, _ *http.Request) {
	out := []robotJSON{}
	for _, s := range h.a.Robots() {
		rj := robotJSON{SerialNumber: s.SerialNumber, OrderID: s.OrderID, LastNodeID: s.LastNodeID, X: s.Position.X, Y: s.Position.Y,
			Driving: s.Driving, StateOfCharge: s.StateOfCharge, Charging: s.Charging, LoadID: s.LoadID, Errors: []string{}}
		for _, e := range s.Errors {
			rj.Errors = append(rj.Errors, e.Type)
		}
		out = append(out, rj)
	}
	writeJSON(w, out)
}

func (h handlers) asrs(w http.ResponseWriter, req *http.Request) {
	v, ok := h.a.Asrs(chi.URLParam(req, "asrsId"))
	if !ok {
		problem(w, http.StatusNotFound, "Not Found", "unknown asrs")
		return
	}
	writeJSON(w, toAsrsJSON(v))
}

func (h handlers) fault(w http.ResponseWriter, req *http.Request) {
	var f faultJSON
	if err := json.NewDecoder(req.Body).Decode(&f); err != nil {
		problem(w, http.StatusBadRequest, "Bad Request", err.Error())
		return
	}
	done(w, h.a.InjectFault(req.Context(), engine.Fault{Kind: engine.FaultKind(f.Kind), Target: f.Target, Aisle: f.Aisle, Port: f.Port, Active: f.Active, Value: f.Value}))
}

func (h handlers) seed(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Position    positionJSON `json:"position"`
		ContainerID string       `json:"containerId"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.ContainerID == "" {
		problem(w, http.StatusBadRequest, "Bad Request", "position and containerId are required")
		return
	}
	p := asrs.Position{Aisle: body.Position.Aisle, Level: body.Position.Level, Slot: body.Position.Slot, Depth: body.Position.Depth}
	done(w, h.a.Seed(chi.URLParam(req, "asrsId"), p, body.ContainerID))
}

func (h handlers) placeAtPort(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ContainerID string `json:"containerId"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.ContainerID == "" {
		problem(w, http.StatusBadRequest, "Bad Request", "containerId is required")
		return
	}
	done(w, h.a.PlaceAtPort(chi.URLParam(req, "asrsId"), chi.URLParam(req, "portId"), body.ContainerID))
}

// done answers 204 for a nil error and the matching problem otherwise.
func done(w http.ResponseWriter, err error) {
	if err != nil {
		commandError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func NewRouter(a Admin, ready func() bool) http.Handler {
	h := handlers{a}
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			problem(w, http.StatusServiceUnavailable, "Service Unavailable", "the MQTT broker is not connected")
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/robots", h.robots)
	r.Get("/asrs/{asrsId}", h.asrs)
	r.Post("/faults", h.fault)
	r.Post("/asrs/{asrsId}/seeds", h.seed)
	r.Post("/asrs/{asrsId}/ports/{portId}/container", h.placeAtPort)
	return r
}
