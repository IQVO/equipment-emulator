package engine

import (
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

// The engine's read-only queries (the admin API's snapshots and the
// telemetry gauges).

func (e *Engine) Robots() []robot.State {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]robot.State, 0, len(e.serials))
	for _, s := range e.serials {
		out = append(out, e.robots[s].Snapshot())
	}
	return out
}

// IsDisconnected reports whether a robot is fault-disconnected (ROBOT_DISCONNECT).
func (e *Engine) IsDisconnected(serial string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.disconnected[serial]
}

func (e *Engine) Asrs(id string) (asrs.View, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.systems[id]
	if !ok {
		return asrs.View{}, false
	}
	return s.Snapshot(), true
}
