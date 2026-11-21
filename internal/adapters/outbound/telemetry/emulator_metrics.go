package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

// meterName scopes the emulator's own instruments, keeping them apart from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/IQVO/equipment-emulator"

// Instrument names (ADR 0005). A metric name is a contract with every
// dashboard built on it: renaming one, or one of its attributes, needs a new ADR.
const (
	ordersReceivedName = "emulator.orders.received"
	telegramsName      = "emulator.telegrams"
	retransmitsName    = "emulator.telegrams.retransmits"
	expiredName        = "emulator.telegrams.expired"
	robotsName         = "emulator.robots"
	simRatioName       = "emulator.simclock.ratio"
)

// Attribute keys. `outcome` and `reason` are reserved for those two concepts
// (fleet convention); the others are bounded catalogues: the VDA 5050 error
// types, the telegram types of protocol v1 and two directions.
const (
	outcomeKey   = attribute.Key("outcome")
	reasonKey    = attribute.Key("reason")
	directionKey = attribute.Key("telegram.direction")
	typeKey      = attribute.Key("telegram.type")
	stateKey     = attribute.Key("robot.state")
)

// Robot states of the emulator.robots gauge, in precedence order.
const (
	StateDisconnected = "disconnected"
	StateError        = "error"
	StateCharging     = "charging"
	StateDriving      = "driving"
	StateIdle         = "idle"
)

var robotStates = []string{StateDisconnected, StateError, StateCharging, StateDriving, StateIdle}

// EmulatorMetrics records the MQTT-side counters. It satisfies the mqtt
// adapter's Metrics port structurally (this package does not import it).
// Until Setup installs a real provider the global one is a no-op, so
// recording is cheap and safe in tests, local runs and with telemetry off.
type EmulatorMetrics struct {
	orders      metric.Int64Counter
	telegrams   metric.Int64Counter
	retransmits metric.Int64Counter
	expired     metric.Int64Counter
}

// NewEmulatorMetrics registers the counters. It only fails on an invalid
// instrument name, a programming error.
func NewEmulatorMetrics() (*EmulatorMetrics, error) {
	meter := otel.Meter(meterName)
	orders, err := meter.Int64Counter(ordersReceivedName,
		metric.WithDescription("VDA 5050 orders received by the simulated robots, by outcome (accepted or rejected); a rejection carries the VDA 5050 error type (or malformed/unsupported) as reason."),
		metric.WithUnit("{order}"))
	if err != nil {
		return nil, err
	}
	telegrams, err := meter.Int64Counter(telegramsName,
		metric.WithDescription("AS/RS telegrams handled by the simulated PLC, by direction (down: received from the WCS; up: first send to the WCS) and type."),
		metric.WithUnit("{telegram}"))
	if err != nil {
		return nil, err
	}
	retransmits, err := meter.Int64Counter(retransmitsName,
		metric.WithDescription("Up telegrams resent because the WCS did not acknowledge them within the handshake timeout, by type."),
		metric.WithUnit("{telegram}"))
	if err != nil {
		return nil, err
	}
	expired, err := meter.Int64Counter(expiredName,
		metric.WithDescription("Up telegrams given up after the last retransmission without an ACK, by type."),
		metric.WithUnit("{telegram}"))
	if err != nil {
		return nil, err
	}
	return &EmulatorMetrics{orders: orders, telegrams: telegrams, retransmits: retransmits, expired: expired}, nil
}

// OrderReceived counts one order; reason is recorded only for a rejection.
func (m *EmulatorMetrics) OrderReceived(ctx context.Context, outcome, reason string) {
	attrs := []attribute.KeyValue{outcomeKey.String(outcome)}
	if reason != "" {
		attrs = append(attrs, reasonKey.String(reason))
	}
	m.orders.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// TelegramSeen counts one telegram received (down) or first sent (up).
func (m *EmulatorMetrics) TelegramSeen(ctx context.Context, direction, telegramType string) {
	m.telegrams.Add(ctx, 1, metric.WithAttributes(directionKey.String(direction), typeKey.String(telegramType)))
}

// TelegramRetransmitted counts one resend of an unacknowledged up telegram.
func (m *EmulatorMetrics) TelegramRetransmitted(ctx context.Context, telegramType string) {
	m.retransmits.Add(ctx, 1, metric.WithAttributes(typeKey.String(telegramType)))
}

// TelegramExpired counts one up telegram given up without an ACK.
func (m *EmulatorMetrics) TelegramExpired(ctx context.Context, telegramType string) {
	m.expired.Add(ctx, 1, metric.WithAttributes(typeKey.String(telegramType)))
}

// RobotSource is what the robots gauge reads: the engine.
type RobotSource interface {
	Robots() []robot.State
	IsDisconnected(serial string) bool
}

// RobotState classifies one robot for the emulator.robots gauge. Precedence:
// fault-disconnected, then an URGENT/CRITICAL/FATAL error (blocked, battery
// empty), then charging, driving, idle. A WARNING (a rejected order) does not
// stop the robot, so it does not make it an error.
func RobotState(s robot.State, disconnected bool) string {
	switch {
	case disconnected:
		return StateDisconnected
	case hasStoppingError(s.Errors):
		return StateError
	case s.Charging:
		return StateCharging
	case s.Driving:
		return StateDriving
	default:
		return StateIdle
	}
}

func hasStoppingError(errs []robot.Error) bool {
	for _, e := range errs {
		if e.Level != robot.LevelWarning {
			return true
		}
	}
	return false
}

// RegisterGauges registers the two observable gauges: robots by state (every
// state reported, zeros included) and the simulated-time ratio.
func RegisterGauges(src RobotSource, ratio func() float64) error {
	meter := otel.Meter(meterName)
	robots, err := meter.Int64ObservableGauge(robotsName,
		metric.WithDescription("Simulated robots by state: disconnected (fault-injected), error, charging, driving or idle."),
		metric.WithUnit("{robot}"))
	if err != nil {
		return err
	}
	simRatio, err := meter.Float64ObservableGauge(simRatioName,
		metric.WithDescription("Simulated seconds elapsed per wall-clock second since the process started; below SIM_SPEED when ticks fall behind."),
		metric.WithUnit("1"))
	if err != nil {
		return err
	}
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		counts := map[string]int64{}
		for _, s := range src.Robots() {
			counts[RobotState(s, src.IsDisconnected(s.SerialNumber))]++
		}
		for _, st := range robotStates {
			o.ObserveInt64(robots, counts[st], metric.WithAttributes(stateKey.String(st)))
		}
		o.ObserveFloat64(simRatio, ratio())
		return nil
	}, robots, simRatio)
	return err
}

// SimRatio returns the simulated-time ratio since the first call's instants:
// simulated time elapsed over wall time elapsed, 0 before any wall time passed.
func SimRatio(simNow, wallNow func() time.Time) func() float64 {
	simStart, wallStart := simNow(), wallNow()
	return func() float64 {
		wall := wallNow().Sub(wallStart).Seconds()
		if wall <= 0 {
			return 0
		}
		return simNow().Sub(simStart).Seconds() / wall
	}
}
