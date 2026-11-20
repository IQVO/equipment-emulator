package mqtt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/equipment-emulator/internal/adapters/vda5050"

	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

type fakeRobots struct {
	mu        sync.Mutex
	handled   []string // orders and instant actions, in the order they ran
	announced []string
	err       error
	delay     time.Duration
}

func (f *fakeRobots) HandleOrder(_ context.Context, s string, o robot.Order) error {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handled = append(f.handled, fmt.Sprintf("%s:order %s/%d", s, o.ID, o.UpdateID))
	return f.err
}

func (f *fakeRobots) HandleInstantActions(_ context.Context, s string, as []robot.Action) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range as {
		f.handled = append(f.handled, s+":"+a.Type)
	}
	return f.err
}

func (f *fakeRobots) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.handled...)
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

func orderJSON(id string, update int) string {
	return fmt.Sprintf(`{"headerId":%d,"timestamp":"2026-11-19T08:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1",`+
		`"orderId":%q,"orderUpdateId":%d,"nodes":[{"nodeId":"n1","sequenceId":0,"released":true,"nodePosition":{"x":0,"y":0,"mapId":"S1"},"actions":[]}],"edges":[]}`,
		update, id, update)
}

const cancelOrderJSON = `{"headerId":9,"timestamp":"2026-11-19T08:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1",` +
	`"actions":[{"actionId":"c1","actionType":"cancelOrder","blockingType":"HARD"}]}`

func TestRobotMessagesThroughTheCallbackAreHandledInArrivalOrder(t *testing.T) {
	f := &fakeRobots{delay: 5 * time.Millisecond}
	g := &RobotGateway{links: map[string]*robotLink{}, cmds: f, log: quietLog()}
	l := newRobotLink(RobotIdentity{"EMU", "R1"}, quietLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g.startWorker(ctx, l)
	h := l.inbox.handler() // the handler paho calls for both topics
	order, instant := vda5050.Topic("EMU", "R1", "order"), vda5050.Topic("EMU", "R1", "instantActions")
	deliver(h, order, orderJSON("o1", 0), orderJSON("o1", 1), orderJSON("o1", 2))
	deliver(h, instant, cancelOrderJSON)
	deliver(h, order, orderJSON("o2", 0))
	deliver(h, "vda5050/v3/EMU/R1/bogus", "{}") // ignored and logged
	want := []string{"R1:order o1/0", "R1:order o1/1", "R1:order o1/2", "R1:cancelOrder", "R1:order o2/0"}
	deadline := time.Now().Add(2 * time.Second)
	for len(f.snapshot()) < len(want) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := f.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("handled %v, want %v", got, want)
	}
}
