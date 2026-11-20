package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
)

// replayClass is the idempotency class of a problem type (docs/idempotency-replay-classes.md,
// ADR 0006). No refusal of the admin API commits state, so there is no OUTCOME: a fault switches
// on or off with `active`, a seed or a port placement is refused before it writes.
type replayClass string

const (
	classFinal     replayClass = "FINAL"
	classRetryable replayClass = "RETRYABLE"
)

// problem is one problem type: status, stable slug (the body's `code`), title and class.
type problem struct {
	status int
	slug   string
	title  string
	class  replayClass
}

const (
	titleBadRequest    = "Bad Request"
	titleNotFound      = "Not Found"
	titleConflict      = "Conflict"
	titleUnprocessable = "Unprocessable Entity"
)

// The catalogue. Ids of robots, AS/RS, ports and rack positions come from the world file and
// never change while the process runs (ADR 0001), so an unknown id or a wrong geometry is FINAL;
// an occupied port or position changes as robots and jobs move, so it is RETRYABLE.
var (
	badRequest            = problem{http.StatusBadRequest, "bad-request", titleBadRequest, classFinal}
	idempotencyKeyInvalid = problem{http.StatusBadRequest, "idempotency-key-invalid", titleBadRequest, classFinal}
	idempotencyKeyReused  = problem{http.StatusConflict, "idempotency-key-reused", titleConflict, classFinal}
	unknownQueryParameter = problem{http.StatusBadRequest, "unknown-query-parameter", titleBadRequest, classFinal}
	invalidLimit          = problem{http.StatusBadRequest, "invalid-limit", titleBadRequest, classFinal}
	invalidCursor         = problem{http.StatusBadRequest, "invalid-cursor", titleBadRequest, classFinal}
	robotNotFound         = problem{http.StatusNotFound, "robot-not-found", titleNotFound, classFinal}
	asrsNotFound          = problem{http.StatusNotFound, "asrs-not-found", titleNotFound, classFinal}
	unknownFaultKind      = problem{http.StatusUnprocessableEntity, "unknown-fault-kind", titleUnprocessable, classFinal}
	// commandNotApplied: the command ran but publishing its result to the broker failed. Nothing
	// is lost by running it again (every admin command converges), so the key is released.
	commandNotApplied = problem{http.StatusUnprocessableEntity, "command-not-applied", titleUnprocessable, classRetryable}
	notReady          = problem{http.StatusServiceUnavailable, "not-ready", "Service Unavailable", classRetryable}
	// asrsRejected covers an AS/RS reject reason no admin route reaches today; unsure means RETRYABLE.
	asrsRejected = problem{http.StatusConflict, "asrs-rejected", titleConflict, classRetryable}
)

// errorProblems maps the engine's typed errors, in match order.
var errorProblems = []struct {
	err error
	p   problem
}{
	{engine.ErrUnknownRobot, robotNotFound},
	{engine.ErrUnknownAsrs, asrsNotFound},
	{engine.ErrUnknownFaultKind, unknownFaultKind},
}

// rejectProblems maps the AS/RS reject reasons the admin routes can answer (409, detail = reason).
var rejectProblems = map[string]problem{
	asrs.ReasonUnknownPort:      {http.StatusConflict, "unknown-port", titleConflict, classFinal},
	asrs.ReasonWrongPortKind:    {http.StatusConflict, "wrong-port-kind", titleConflict, classFinal},
	asrs.ReasonInvalidPosition:  {http.StatusConflict, "invalid-position", titleConflict, classFinal},
	asrs.ReasonPortOccupied:     {http.StatusConflict, "port-occupied", titleConflict, classRetryable},
	asrs.ReasonPositionOccupied: {http.StatusConflict, "position-occupied", titleConflict, classRetryable},
}

// allProblems lists every problem type once, the order fixed for the tests.
func allProblems() []problem {
	out := []problem{badRequest, idempotencyKeyInvalid, idempotencyKeyReused, unknownQueryParameter, invalidLimit,
		invalidCursor, unknownFaultKind, commandNotApplied, notReady, asrsRejected}
	for _, e := range errorProblems {
		if e.p != unknownFaultKind {
			out = append(out, e.p)
		}
	}
	for _, reason := range []string{asrs.ReasonUnknownPort, asrs.ReasonWrongPortKind, asrs.ReasonInvalidPosition,
		asrs.ReasonPortOccupied, asrs.ReasonPositionOccupied} {
		out = append(out, rejectProblems[reason])
	}
	return out
}

// problemFor maps a command error to its problem and the detail the body carries.
func problemFor(err error) (problem, string) {
	for _, e := range errorProblems {
		if errors.Is(err, e.err) {
			return e.p, err.Error()
		}
	}
	var re *asrs.RejectError
	if errors.As(err, &re) {
		if p, ok := rejectProblems[re.Reason]; ok {
			return p, re.Reason
		}
		return asrsRejected, re.Reason
	}
	return commandNotApplied, err.Error()
}

// problemBody is the RFC 7807 document; Code is the stable slug.
type problemBody struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
}

// storeMarker is the idempotency middleware's capturing writer: a RETRYABLE answer is marked so
// the middleware releases the key instead of storing the answer.
type storeMarker interface{ doNotStore() }

// writeProblem writes p; a RETRYABLE problem marks the capturing writer first.
func writeProblem(w http.ResponseWriter, _ *http.Request, p problem, detail string) {
	if m, ok := w.(storeMarker); ok && p.class == classRetryable {
		m.doNotStore()
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemBody{Type: "about:blank", Title: p.title, Status: p.status, Detail: detail, Code: p.slug})
}

// writeError answers a command error with its catalogue problem.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	p, detail := problemFor(err)
	writeProblem(w, r, p, detail)
}
