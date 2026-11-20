package mqtt

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

type fakeRobots struct {
	mu        sync.Mutex
	orders    []string
	instant   []string
	announced []string
	err       error
}

func (f *fakeRobots) HandleOrder(_ context.Context, s string, o robot.Order) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orders = append(f.orders, s+":"+o.ID)
	return f.err
}

func (f *fakeRobots) HandleInstantActions(_ context.Context, s string, as []robot.Action) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range as {
		f.instant = append(f.instant, s+":"+a.Type)
	}
	return f.err
}

func (f *fakeRobots) AnnounceRobot(_ context.Context, s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.announced = append(f.announced, s)
	return f.err
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestReannounceGoesThroughTheEngine(t *testing.T) {
	f := &fakeRobots{}
	g := &RobotGateway{links: map[string]*robotLink{}, cmds: f, log: quietLog()}
	l := &robotLink{id: RobotIdentity{"EMU", "R1"}, headers: map[string]uint32{}}
	g.reannounce(l)
	f.err = errors.New("boom") // a failure is logged, not fatal
	g.reannounce(l)
	if len(f.announced) != 2 || f.announced[0] != "R1" {
		t.Fatalf("announced = %v", f.announced)
	}
}
