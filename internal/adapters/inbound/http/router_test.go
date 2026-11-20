package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

type fakeAdmin struct {
	faults []engine.Fault
	seeds  []string
}

func (f *fakeAdmin) Robots() []robot.State { return []robot.State{{SerialNumber: "R1"}} }
func (f *fakeAdmin) Asrs(id string) (asrs.View, bool) {
	if id == "EMPTY" {
		return asrs.View{ID: id}, true
	}
	return asrs.View{ID: id, Slots: map[string]string{"A1-L1-S1-D1": "C1"}, Ports: map[string]string{"OUT1": ""},
		Units: []asrs.UnitView{{ID: "A1", State: asrs.UnitExecute, CurrentJob: "j1", Queued: 2}}}, id == "ASRS1"
}
func (f *fakeAdmin) InjectFault(_ context.Context, fl engine.Fault) error {
	if fl.Target == "NOPE" {
		return engine.ErrUnknownRobot
	}
	f.faults = append(f.faults, fl)
	return nil
}
func (f *fakeAdmin) Seed(id string, p asrs.Position, c string) error {
	f.seeds = append(f.seeds, id+":"+p.String()+":"+c)
	return nil
}
func (f *fakeAdmin) PlaceAtPort(string, string, string) error {
	return &asrs.RejectError{Reason: asrs.ReasonPortOccupied}
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestRoutes(t *testing.T) {
	a := &fakeAdmin{}
	h := NewRouter(a, func() bool { return false })
	if r := do(h, "GET", "/healthz", ""); r.Code != 200 {
		t.Fatalf("healthz %d", r.Code)
	}
	if r := do(h, "GET", "/readyz", ""); r.Code != 503 {
		t.Fatalf("readyz %d", r.Code)
	}
	if r := do(h, "GET", "/robots", ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"serialNumber":"R1"`) {
		t.Fatalf("robots %d %s", r.Code, r.Body)
	}
	if r := do(h, "GET", "/asrs/NOPE", ""); r.Code != 404 || r.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("asrs 404 %d %s", r.Code, r.Header())
	}
	if r := do(h, "POST", "/faults", `{"kind":"ROBOT_BLOCKED","target":"R1","active":true}`); r.Code != 204 || len(a.faults) != 1 {
		t.Fatalf("fault %d", r.Code)
	}
	if r := do(h, "POST", "/faults", `{"kind":"ROBOT_BLOCKED","target":"NOPE","active":true}`); r.Code != 404 {
		t.Fatalf("unknown target %d", r.Code)
	}
	if r := do(h, "POST", "/faults", `{`); r.Code != 400 {
		t.Fatalf("bad json %d", r.Code)
	}
	if r := do(h, "POST", "/asrs/ASRS1/seeds", `{"position":{"aisle":1,"level":1,"slot":1,"depth":1},"containerId":"C1"}`); r.Code != 204 || a.seeds[0] != "ASRS1:A1-L1-S1-D1:C1" {
		t.Fatalf("seed %d %v", r.Code, a.seeds)
	}
	if r := do(h, "POST", "/asrs/ASRS1/ports/IN1/container", `{"containerId":"C2"}`); r.Code != 409 || !strings.Contains(r.Body.String(), "PORT_OCCUPIED") {
		t.Fatalf("port %d %s", r.Code, r.Body)
	}
}

func TestGetAsrsBodyMatchesTheOpenAPIShape(t *testing.T) {
	h := NewRouter(&fakeAdmin{}, func() bool { return true })
	r := do(h, "GET", "/asrs/ASRS1", "")
	if r.Code != 200 || r.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("asrs %d %s", r.Code, r.Header())
	}
	var got map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"id":    "ASRS1",
		"slots": map[string]any{"A1-L1-S1-D1": "C1"},
		"ports": map[string]any{"OUT1": ""},
		"units": []any{map[string]any{"id": "A1", "state": "EXECUTE", "currentJob": "j1", "queued": float64(2)}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %s", r.Body)
	}
	// required fields are present even for an empty view
	r = do(h, "GET", "/asrs/EMPTY", "")
	if strings.TrimSpace(r.Body.String()) != `{"id":"EMPTY","slots":{},"ports":{},"units":[]}` {
		t.Fatalf("empty body = %s", r.Body)
	}
}
