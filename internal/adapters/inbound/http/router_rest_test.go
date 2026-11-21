package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

// worldAdmin is a fakeAdmin with a configurable robot list and port answers.
type worldAdmin struct {
	fakeAdmin
	serials []string
	places  int
	portErr error
}

func (w *worldAdmin) Robots() []robot.State {
	out := make([]robot.State, 0, len(w.serials))
	for _, s := range w.serials {
		out = append(out, robot.State{SerialNumber: s})
	}
	return out
}

func (w *worldAdmin) PlaceAtPort(string, string, string) error {
	w.places++
	return w.portErr
}

func doKeyed(h http.Handler, method, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(IdempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func router(a Admin) http.Handler {
	return NewRouter(a, func() bool { return true }, quietLogger(), "equipment-emulator")
}

func TestAKeyedFaultIsAppliedOnceAndReplayed(t *testing.T) {
	a := &worldAdmin{}
	h := router(a)
	body := `{"kind":"ROBOT_BLOCKED","target":"R1","active":true}`
	first := doKeyed(h, "POST", "/faults", body, "f-1")
	again := doKeyed(h, "POST", "/faults", body, "f-1")
	if first.Code != 204 || again.Code != 204 || len(a.faults) != 1 {
		t.Fatalf("codes %d/%d, faults applied %d, want 204/204 and 1", first.Code, again.Code, len(a.faults))
	}
	if first.Header().Get(IdempotencyReplayedHeader) != "" || again.Header().Get(IdempotencyReplayedHeader) != "true" {
		t.Fatal("only the second answer is a replay")
	}
	other := doKeyed(h, "POST", "/faults", `{"kind":"ROBOT_BLOCKED","target":"R1","active":false}`, "f-1")
	if other.Code != 409 || !strings.Contains(other.Body.String(), `"code":"idempotency-key-reused"`) || len(a.faults) != 1 {
		t.Fatalf("another body under the key = %d %s", other.Code, other.Body)
	}
	if r := do(h, "POST", "/faults", body); r.Code != 204 || len(a.faults) != 2 {
		t.Fatalf("an unkeyed POST must run: %d, faults %d", r.Code, len(a.faults))
	}
}

// PORT_OCCUPIED depends on state a robot changes (it picks the container), so
// it is RETRYABLE: the key is released and the retry runs.
func TestARetryableRefusalReleasesTheKey(t *testing.T) {
	a := &worldAdmin{portErr: &asrs.RejectError{Reason: asrs.ReasonPortOccupied}}
	h := router(a)
	path, body := "/asrs/ASRS1/ports/IN1/container", `{"containerId":"C1"}`
	if r := doKeyed(h, "POST", path, body, "p-1"); r.Code != 409 || !strings.Contains(r.Body.String(), `"code":"port-occupied"`) {
		t.Fatalf("first = %d %s", r.Code, r.Body)
	}
	a.portErr = nil // the robot took the container away
	if r := doKeyed(h, "POST", path, body, "p-1"); r.Code != 204 || r.Header().Get(IdempotencyReplayedHeader) != "" || a.places != 2 {
		t.Fatalf("retry = %d replayed=%q places=%d, want a fresh 204", r.Code, r.Header().Get(IdempotencyReplayedHeader), a.places)
	}
}

// The world is fixed at boot (ADR 0001): an unknown AS/RS stays unknown, so the
// 404 is FINAL and replayed.
func TestAFinalRefusalIsReplayed(t *testing.T) {
	a := &worldAdmin{portErr: engine.ErrUnknownAsrs}
	h := router(a)
	first := doKeyed(h, "POST", "/asrs/NOPE/ports/IN1/container", `{"containerId":"C1"}`, "n-1")
	again := doKeyed(h, "POST", "/asrs/NOPE/ports/IN1/container", `{"containerId":"C1"}`, "n-1")
	if first.Code != 404 || again.Code != 404 || again.Header().Get(IdempotencyReplayedHeader) != "true" || a.places != 1 {
		t.Fatalf("codes %d/%d replayed=%q places=%d", first.Code, again.Code, again.Header().Get(IdempotencyReplayedHeader), a.places)
	}
	if again.Body.String() != first.Body.String() || !strings.Contains(first.Body.String(), `"code":"asrs-not-found"`) {
		t.Fatalf("replayed body %s, first %s", again.Body, first.Body)
	}
}

func TestEveryProblemBodyCarriesItsCode(t *testing.T) {
	h := router(&fakeAdmin{})
	for _, tc := range []struct{ method, path, body, code string }{
		{"GET", "/asrs/NOPE", "", "asrs-not-found"},
		{"POST", "/faults", `{`, "bad-request"},
		{"POST", "/faults", `{"kind":"ROBOT_BLOCKED","target":"NOPE","active":true}`, "robot-not-found"},
		{"POST", "/asrs/ASRS1/ports/IN1/container", `{"containerId":"C2"}`, "port-occupied"},
		{"GET", "/robots?after=R1", "", "unknown-query-parameter"},
	} {
		r := do(h, tc.method, tc.path, tc.body)
		var p map[string]any
		if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		if p["code"] != tc.code || p["type"] != "about:blank" || p["status"] != float64(r.Code) || r.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s %s = %d %v, want code %s", tc.method, tc.path, r.Code, p, tc.code)
		}
	}
}

func TestProblemForMapsEachErrorToItsCatalogueEntry(t *testing.T) {
	cases := []struct {
		err  error
		slug string
	}{
		{engine.ErrUnknownRobot, "robot-not-found"},
		{engine.ErrUnknownAsrs, "asrs-not-found"},
		{engine.ErrUnknownFaultKind, "unknown-fault-kind"},
		{&asrs.RejectError{Reason: asrs.ReasonUnknownPort}, "unknown-port"},
		{&asrs.RejectError{Reason: asrs.ReasonWrongPortKind}, "wrong-port-kind"},
		{&asrs.RejectError{Reason: asrs.ReasonInvalidPosition}, "invalid-position"},
		{&asrs.RejectError{Reason: asrs.ReasonPortOccupied}, "port-occupied"},
		{&asrs.RejectError{Reason: asrs.ReasonPositionOccupied}, "position-occupied"},
		{&asrs.RejectError{Reason: asrs.ReasonDuplicateJob}, "asrs-rejected"},
		{context.DeadlineExceeded, "command-not-applied"},
	}
	for _, tc := range cases {
		if p, _ := problemFor(tc.err); p.slug != tc.slug {
			t.Errorf("problemFor(%v) = %s, want %s", tc.err, p.slug, tc.slug)
		}
	}
}

func listRobots(t *testing.T, h http.Handler, query string) (int, map[string]any) {
	t.Helper()
	r := do(h, "GET", "/robots"+query, "")
	var body map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET /robots%s: %v (%s)", query, err, r.Body)
	}
	return r.Code, body
}

func serialsOf(body map[string]any) []string {
	var out []string
	items, _ := body["items"].([]any)
	for _, it := range items {
		out = append(out, it.(map[string]any)["serialNumber"].(string))
	}
	return out
}

func TestRobotsPageByKeysetCursor(t *testing.T) {
	h := router(&worldAdmin{serials: []string{"R3", "R1", "R5", "R2", "R4"}})
	code, first := listRobots(t, h, "?limit=2")
	if code != 200 || strings.Join(serialsOf(first), ",") != "R1,R2" || first["nextCursor"] == nil {
		t.Fatalf("first page = %d %v", code, first)
	}
	var all []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		q := "?limit=2"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		_, body := listRobots(t, h, q)
		all = append(all, serialsOf(body)...)
		next, ok := body["nextCursor"].(string)
		if !ok {
			break
		}
		cursor = next
	}
	if strings.Join(all, ",") != "R1,R2,R3,R4,R5" {
		t.Fatalf("walked %v, want every robot once in serial order", all)
	}
	if _, body := listRobots(t, h, ""); len(serialsOf(body)) != 5 || body["nextCursor"] != nil {
		t.Fatalf("default page = %v, want all five and no nextCursor", body)
	}
	if _, body := listRobots(t, router(&worldAdmin{}), ""); body["items"] == nil {
		t.Fatal("an empty list must be items: [], never null")
	}
}

func TestRobotsRefuseBadPagingParameters(t *testing.T) {
	h := router(&worldAdmin{serials: []string{"R1"}})
	for query, slug := range map[string]string{
		"?limit=0": "invalid-limit", "?limit=501": "invalid-limit", "?limit=": "invalid-limit",
		"?limit=1&limit=2": "invalid-limit", "?limit=x": "invalid-limit",
		"?cursor=@@": "invalid-cursor", "?cursor=a&cursor=b": "invalid-cursor",
		"?cursor=eyJzIjoiUjEiLCJpIjoiUjIifQ": "invalid-cursor", // sort and id differ
		"?offset=1":                          "unknown-query-parameter",
	} {
		if code, body := listRobots(t, h, query); code != 400 || body["code"] != slug {
			t.Errorf("GET /robots%s = %d %v, want 400 %s", query, code, body, slug)
		}
	}
	if code, _ := listRobots(t, h, "?limit=500"); code != 200 {
		t.Errorf("limit=500 = %d, want 200", code)
	}
}

// The ADR's class table is the catalogue: a new or reclassified problem type
// must change both.
func TestADR0006ClassTableEqualsTheCatalogue(t *testing.T) {
	b, err := os.ReadFile("../../../../docs/adr/0006-rest-guideline-on-the-admin-api.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^ *\\| `([a-z-]+)` \\| ([0-9]{3}) \\| (FINAL|OUTCOME|RETRYABLE) \\|")
	var inADR, inCode []string
	for _, m := range row.FindAllStringSubmatch(string(b), -1) {
		inADR = append(inADR, m[1]+" "+m[2]+" "+m[3])
	}
	for _, p := range allProblems() {
		inCode = append(inCode, p.slug+" "+strconv.Itoa(p.status)+" "+string(p.class))
	}
	sort.Strings(inADR)
	sort.Strings(inCode)
	if strings.Join(inADR, "\n") != strings.Join(inCode, "\n") {
		t.Fatalf("ADR 0006 class table:\n%s\n\ncatalogue:\n%s", strings.Join(inADR, "\n"), strings.Join(inCode, "\n"))
	}
}
