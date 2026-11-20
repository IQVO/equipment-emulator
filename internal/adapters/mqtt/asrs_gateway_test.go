package mqtt

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IQVO/equipment-emulator/internal/adapters/telegram"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
)

type fakeAsrs struct {
	mu       sync.Mutex
	submits  int
	cancels  []string
	inflight atomic.Int32
	maxSeen  atomic.Int32
	delay    time.Duration
}

func (f *fakeAsrs) SubmitJob(string, asrs.Job) error {
	n := f.inflight.Add(1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(f.delay)
	f.inflight.Add(-1)
	f.mu.Lock()
	f.submits++
	f.mu.Unlock()
	return nil
}
func (f *fakeAsrs) CancelJob(_, id string) error {
	f.mu.Lock()
	f.cancels = append(f.cancels, id)
	f.mu.Unlock()
	return nil
}
func (f *fakeAsrs) UnitStates(context.Context, string) error { return nil }

func testGateway(cmds AsrsCommands) (*AsrsGateway, *[]string) {
	var mu sync.Mutex
	sent := []string{}
	return &AsrsGateway{asrsID: "A1", cmds: cmds, dedup: telegram.NewDedup(100),
		retx: telegram.NewRetransmitter(2*time.Second, 3), now: time.Now,
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		pub: func(_ string, b []byte) error { mu.Lock(); sent = append(sent, string(b)); mu.Unlock(); return nil }}, &sent
}

const storeJSON = `{"telegramId":"s1","type":"STORE","timestamp":"2026-11-19T08:00:00.000Z","jobId":"j1","containerId":"C1","portId":"IN1","to":{"aisle":1,"level":1,"slot":1,"depth":1}}`

func TestMalformedTelegramNackIsRememberedAndResent(t *testing.T) {
	g, _ := testGateway(&fakeAsrs{})
	bad := []byte(`{"telegramId":"m1","type":"BOGUS","timestamp":"2026-11-19T08:00:00.000Z"}`)
	first, _ := g.process(context.Background(), bad)
	second, _ := g.process(context.Background(), bad)
	if first == nil || second == nil {
		t.Fatal("both deliveries must be answered")
	}
	if first.ID != second.ID || first.AckOf != "m1" {
		t.Fatalf("retransmit got a new NACK: %q vs %q", first.ID, second.ID)
	}
}

func TestDuplicateStoreSubmitsOnceAndRepeatsAck(t *testing.T) {
	f := &fakeAsrs{}
	g, _ := testGateway(f)
	first, _ := g.process(context.Background(), []byte(storeJSON))
	second, _ := g.process(context.Background(), []byte(storeJSON))
	if f.submits != 1 {
		t.Fatalf("SubmitJob called %d times", f.submits)
	}
	if first.ID != second.ID || first.Accepted == nil || !*first.Accepted {
		t.Fatalf("expected the same accepted ACK twice: %+v %+v", first, second)
	}
}

func TestHandleSerialisesConcurrentTelegrams(t *testing.T) {
	f := &fakeAsrs{delay: 10 * time.Millisecond}
	g, _ := testGateway(f)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := []byte(`{"telegramId":"s` + string(rune('a'+i)) + `","type":"STORE","timestamp":"2026-11-19T08:00:00.000Z","jobId":"j","containerId":"C","portId":"P","to":{"aisle":1,"level":1,"slot":1,"depth":1}}`)
			g.handle(context.Background(), p)
		}(i)
	}
	wg.Wait()
	if m := f.maxSeen.Load(); m != 1 {
		t.Fatalf("max concurrent SubmitJob = %d, want 1", m)
	}
}
