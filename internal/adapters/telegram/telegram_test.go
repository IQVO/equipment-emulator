package telegram

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecodeStore(t *testing.T) {
	raw := `{"telegramId":"t1","type":"STORE","timestamp":"2026-11-19T08:00:00.000Z","jobId":"j1","containerId":"C1","portId":"IN1","to":{"aisle":1,"level":2,"slot":10,"depth":1}}`
	tg, err := Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if tg.Type != Store || tg.To == nil || tg.To.Level != 2 || tg.PortID != "IN1" {
		t.Fatalf("telegram = %+v", tg)
	}
}

func TestDecodeRejectsMissingFields(t *testing.T) {
	cases := map[string]string{
		"no id":    `{"type":"STORE","timestamp":"2026-11-19T08:00:00.000Z"}`,
		"bad type": `{"telegramId":"t","type":"DANCE","timestamp":"2026-11-19T08:00:00.000Z"}`,
		"store/to": `{"telegramId":"t","type":"STORE","timestamp":"2026-11-19T08:00:00.000Z","jobId":"j","containerId":"C","portId":"IN1"}`,
		"ack/of":   `{"telegramId":"t","type":"ACK","timestamp":"2026-11-19T08:00:00.000Z","accepted":true}`,
		"not json": `{`,
	}
	for name, raw := range cases {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestEncodeRoundTripAndMillis(t *testing.T) {
	at := time.Date(2026, 11, 19, 8, 0, 0, 123456789, time.UTC)
	b, err := Encode(Telegram{ID: "t2", Type: PortArrival, Timestamp: at, PortID: "OUT1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"timestamp":"2026-11-19T08:00:00.123Z"`) || strings.Contains(string(b), "containerId") {
		t.Fatalf("encoded = %s", b)
	}
}

func TestNewAck(t *testing.T) {
	in := Telegram{ID: "t3", Type: Cancel, JobID: "j"}
	ok := NewAck(in, nil, "a1", time.Unix(0, 0))
	if ok.Type != Ack || ok.AckOf != "t3" || ok.Accepted == nil || !*ok.Accepted || ok.Reason != "" {
		t.Fatalf("ack = %+v", ok)
	}
	no := NewAck(in, errors.New("UNKNOWN_JOB"), "a2", time.Unix(0, 0))
	if *no.Accepted || no.Reason != "UNKNOWN_JOB" {
		t.Fatalf("nack = %+v", no)
	}
}

func TestTopics(t *testing.T) {
	if DownTopic("ASRS1") != "asrs/v1/ASRS1/down" || UpTopic("ASRS1") != "asrs/v1/ASRS1/up" {
		t.Fatal("topics")
	}
}
