package telemetry_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/IQVO/equipment-emulator/internal/adapters/outbound/telemetry"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

// withReader installs a ManualReader MeterProvider for the test and restores
// the previous global provider afterwards.
func withReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	return reader
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	out := map[string]metricdata.Metrics{}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

// attrKey renders a data point's attributes as "k=v,k=v" in key order.
func attrKey(set attribute.Set) string {
	var parts []string
	iter := set.Iter()
	for iter.Next() {
		kv := iter.Attribute()
		parts = append(parts, string(kv.Key)+"="+kv.Value.String())
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// counterPoints returns a monotonic int64 counter's value per attribute set.
func counterPoints(t *testing.T, got map[string]metricdata.Metrics, name, unit string) map[string]int64 {
	t.Helper()
	m, ok := got[name]
	if !ok {
		t.Fatalf("no %s series was exported", name)
	}
	if m.Unit != unit {
		t.Errorf("%s unit = %q, want %q", name, m.Unit, unit)
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok || !sum.IsMonotonic {
		t.Fatalf("%s is a %T (monotonic=%v), want a monotonic int64 Sum", name, m.Data, ok && sum.IsMonotonic)
	}
	out := map[string]int64{}
	for _, p := range sum.DataPoints {
		out[attrKey(p.Attributes)] = p.Value
	}
	return out
}

func assertPoints(t *testing.T, name string, got, want map[string]int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s has %d series %v, want %d %v", name, len(got), got, len(want), want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s{%s} = %d, want %d", name, k, got[k], v)
		}
	}
}

func TestEmulatorMetrics_OrdersByOutcome(t *testing.T) {
	reader := withReader(t)
	m, err := telemetry.NewEmulatorMetrics()
	if err != nil {
		t.Fatalf("NewEmulatorMetrics: %v", err)
	}
	ctx := context.Background()
	m.OrderReceived(ctx, "accepted", "")
	m.OrderReceived(ctx, "accepted", "")
	m.OrderReceived(ctx, "rejected", "INVALID_ORDER")

	got := counterPoints(t, collect(t, reader), "emulator.orders.received", "{order}")
	assertPoints(t, "emulator.orders.received", got, map[string]int64{
		"outcome=accepted":                      2,
		"outcome=rejected,reason=INVALID_ORDER": 1,
	})
}

func TestEmulatorMetrics_TelegramsRetransmitsAndExpiries(t *testing.T) {
	reader := withReader(t)
	m, err := telemetry.NewEmulatorMetrics()
	if err != nil {
		t.Fatalf("NewEmulatorMetrics: %v", err)
	}
	ctx := context.Background()
	m.TelegramSeen(ctx, "down", "STORE")
	m.TelegramSeen(ctx, "up", "ACK")
	m.TelegramSeen(ctx, "up", "ACK")
	m.TelegramRetransmitted(ctx, "JOB_DONE")
	m.TelegramExpired(ctx, "JOB_DONE")

	got := collect(t, reader)
	assertPoints(t, "emulator.telegrams", counterPoints(t, got, "emulator.telegrams", "{telegram}"), map[string]int64{
		"telegram.direction=down,telegram.type=STORE": 1,
		"telegram.direction=up,telegram.type=ACK":     2,
	})
	assertPoints(t, "emulator.telegrams.retransmits", counterPoints(t, got, "emulator.telegrams.retransmits", "{telegram}"),
		map[string]int64{"telegram.type=JOB_DONE": 1})
	assertPoints(t, "emulator.telegrams.expired", counterPoints(t, got, "emulator.telegrams.expired", "{telegram}"),
		map[string]int64{"telegram.type=JOB_DONE": 1})
}

type fakeRobots struct {
	states       []robot.State
	disconnected map[string]bool
}

func (f fakeRobots) Robots() []robot.State             { return f.states }
func (f fakeRobots) IsDisconnected(serial string) bool { return f.disconnected[serial] }

func TestRegisterGauges_RobotsByStateAndSimulatedTimeRatio(t *testing.T) {
	reader := withReader(t)
	src := fakeRobots{
		states: []robot.State{
			{SerialNumber: "R1", Driving: true},
			{SerialNumber: "R2", Charging: true},
			{SerialNumber: "R3"},
			{SerialNumber: "R4", Driving: true, Errors: []robot.Error{{Type: robot.ErrRobotBlocked, Level: robot.LevelCritical}}},
			{SerialNumber: "R5", Driving: true},
			{SerialNumber: "R6", Errors: []robot.Error{{Type: robot.ErrValidation, Level: robot.LevelWarning}}},
		},
		disconnected: map[string]bool{"R5": true},
	}
	if err := telemetry.RegisterGauges(src, func() float64 { return 9.5 }); err != nil {
		t.Fatalf("RegisterGauges: %v", err)
	}
	got := collect(t, reader)

	robots, ok := got["emulator.robots"]
	if !ok {
		t.Fatal("no emulator.robots series was exported")
	}
	if robots.Unit != "{robot}" {
		t.Errorf("emulator.robots unit = %q, want {robot}", robots.Unit)
	}
	gauge, ok := robots.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("emulator.robots is a %T, want an int64 Gauge", robots.Data)
	}
	byState := map[string]int64{}
	for _, p := range gauge.DataPoints {
		byState[attrKey(p.Attributes)] = p.Value
	}
	// Every state is reported, zeros included, so a dashboard never sees a
	// series vanish when the last robot leaves a state.
	assertPoints(t, "emulator.robots", byState, map[string]int64{
		"robot.state=driving": 1, "robot.state=charging": 1, "robot.state=idle": 2,
		"robot.state=error": 1, "robot.state=disconnected": 1,
	})

	ratio, ok := got["emulator.simclock.ratio"]
	if !ok {
		t.Fatal("no emulator.simclock.ratio series was exported")
	}
	if ratio.Unit != "1" {
		t.Errorf("emulator.simclock.ratio unit = %q, want 1", ratio.Unit)
	}
	fg, ok := ratio.Data.(metricdata.Gauge[float64])
	if !ok || len(fg.DataPoints) != 1 || fg.DataPoints[0].Value != 9.5 {
		t.Fatalf("emulator.simclock.ratio = %+v, want one point of 9.5", ratio.Data)
	}
}

func TestRobotState_Precedence(t *testing.T) {
	critical := []robot.Error{{Type: robot.ErrBatteryEmpty, Level: robot.LevelFatal}}
	cases := []struct {
		name         string
		s            robot.State
		disconnected bool
		want         string
	}{
		{"disconnected wins over everything", robot.State{Driving: true, Errors: critical}, true, "disconnected"},
		{"a fatal error wins over driving", robot.State{Driving: true, Errors: critical}, false, "error"},
		{"an urgent error is an error", robot.State{Errors: []robot.Error{{Level: robot.LevelUrgent}}}, false, "error"},
		{"a warning is not an error", robot.State{Errors: []robot.Error{{Level: robot.LevelWarning}}}, false, "idle"},
		{"charging wins over driving", robot.State{Driving: true, Charging: true}, false, "charging"},
		{"driving", robot.State{Driving: true}, false, "driving"},
		{"idle", robot.State{}, false, "idle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := telemetry.RobotState(tc.s, tc.disconnected); got != tc.want {
				t.Errorf("RobotState = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSimRatio(t *testing.T) {
	simStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	wallStart := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	sim, wall := simStart, wallStart
	ratio := telemetry.SimRatio(func() time.Time { return sim }, func() time.Time { return wall })

	if got := ratio(); got != 0 {
		t.Errorf("ratio before any wall time passed = %v, want 0", got)
	}
	sim, wall = simStart.Add(50*time.Second), wallStart.Add(10*time.Second)
	if got := ratio(); got != 5 {
		t.Errorf("ratio = %v, want 5 (50 simulated s in 10 wall s)", got)
	}
}
