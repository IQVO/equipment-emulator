package mqtt

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/equipment-emulator/internal/adapters/telegram"
)

// Metrics is the port the gateways record their counters through (ADR 0005),
// implemented by internal/adapters/outbound/telemetry and wired by cmd/emulator.
// A gateway without one records nothing.
type Metrics interface {
	// OrderReceived counts one VDA 5050 order: outcome accepted or rejected;
	// a rejection carries a bounded reason (a VDA 5050 error type, NOT_READY,
	// ROBOT_DISCONNECTED, UNKNOWN_ROBOT, malformed or unsupported).
	OrderReceived(ctx context.Context, outcome, reason string)
	// TelegramSeen counts one telegram received (down) or first sent (up).
	TelegramSeen(ctx context.Context, direction, telegramType string)
	// TelegramRetransmitted counts one resend of an unacknowledged up telegram.
	TelegramRetransmitted(ctx context.Context, telegramType string)
	// TelegramExpired counts one up telegram given up without an ACK.
	TelegramExpired(ctx context.Context, telegramType string)
}

type noMetrics struct{}

func (noMetrics) OrderReceived(context.Context, string, string) {}
func (noMetrics) TelegramSeen(context.Context, string, string)  {}
func (noMetrics) TelegramRetransmitted(context.Context, string) {}
func (noMetrics) TelegramExpired(context.Context, string)       {}

func metricsOrNoop(m Metrics) Metrics {
	if m == nil {
		return noMetrics{}
	}
	return m
}

// Order outcomes and the gateway's own rejection reasons.
const (
	outcomeAccepted   = "accepted"
	outcomeRejected   = "rejected"
	reasonMalformed   = "malformed"
	reasonUnsupported = "unsupported"
)

// Telegram directions.
const (
	directionDown = "down"
	directionUp   = "up"
)

// unknownType labels a telegram whose type is not one of protocol v1's, so
// a sender's typo cannot grow a metric's cardinality.
const unknownType = "UNKNOWN"

var knownTypes = map[telegram.Type]bool{
	telegram.Store: true, telegram.Retrieve: true, telegram.Move: true, telegram.Cancel: true,
	telegram.StateRequest: true, telegram.Ack: true, telegram.JobDone: true, telegram.JobFailed: true,
	telegram.UnitState: true, telegram.PortArrival: true,
}

func typeLabel(t telegram.Type) string {
	if knownTypes[t] {
		return string(t)
	}
	return unknownType
}

// tracerName is the instrumentation scope of every MQTT span.
const tracerName = "github.com/IQVO/equipment-emulator/internal/adapters/mqtt"

// Span attribute keys. MQTT 3.1.1 has no message headers and neither VDA 5050
// nor the telegram payload carries a trace context, so a receive span is the
// root of its trace (ADR 0005).
const (
	attrSystem      = attribute.Key("messaging.system")
	attrDestination = attribute.Key("messaging.destination.name")
	attrSerial      = attribute.Key("vda5050.serial_number")
	attrMessageType = attribute.Key("vda5050.message.type")
	attrAsrsID      = attribute.Key("asrs.id")
	attrTelegramID  = attribute.Key("telegram.id")
	attrTelegramTyp = attribute.Key("telegram.type")
	attrRetransmit  = attribute.Key("telegram.retransmit")
)

// startSpan opens a span of the MQTT adapter. The tracer is looked up per call
// so a provider installed after the gateway was built still applies.
func startSpan(ctx context.Context, name string, kind trace.SpanKind, topic string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	attrs = append([]attribute.KeyValue{attrSystem.String("mqtt"), attrDestination.String(topic)}, attrs...)
	return otel.Tracer(tracerName).Start(ctx, name, trace.WithSpanKind(kind), trace.WithAttributes(attrs...))
}

// endSpan records err (when not nil) on span and ends it.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
