//go:build integration

package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IQVO/equipment-emulator/internal/adapters/telegram"
	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

func broker(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{Started: true,
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "eclipse-mosquitto:2.0.20", ExposedPorts: []string{"1883/tcp"},
			Cmd:        []string{"mosquitto", "-c", "/mosquitto-no-auth.conf"},
			WaitingFor: wait.ForListeningPort("1883/tcp"),
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "1883")
	return fmt.Sprintf("tcp://%s:%s", host, port.Port())
}

func TestRobotAndAsrsOverMQTT(t *testing.T) {
	url := broker(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := engine.World{
		Robots: []engine.RobotSpec{{Manufacturer: "EMU", SerialNumber: "R1", MapID: "S1", Speed: 1, ActionSeconds: 0.1}},
		Asrs: []engine.AsrsSpec{{ID: "ASRS1", Aisles: 1, Levels: 2, Slots: 2, BaseSeconds: 0.1,
			Ports: []engine.PortSpec{{ID: "OUT1", Aisle: 1, Kind: "OUTPUT"}}}},
	}
	clock, robots, systems, nodes, err := w.Build(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.Default()
	router := AsrsRouter{}
	var eng *engine.Engine
	rg, err := NewRobotGateway(url, []RobotIdentity{{"EMU", "R1"}}, cmdsProxy{&eng}, clock.Now, log)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := NewAsrsGateway(url, "ASRS1", asrsProxy{&eng}, clock.Now, log)
	if err != nil {
		t.Fatal(err)
	}
	router["ASRS1"] = ag
	eng = engine.New(clock, robots, systems, nodes, rg, router)
	_ = eng.Seed("ASRS1", asrsPos(1, 1), "C1")
	if err := rg.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ag.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// a WCS-side test client
	wcs, err := Connect(url, "test-wcs", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 64)
	for topic, prefix := range map[string]string{"vda5050/v3/EMU/R1/state": "state:", telegram.UpTopic("ASRS1"): "up:"} {
		if err := subscribe(wcs, topic, func(_ pahomqtt.Client, m pahomqtt.Message) {
			select { // never block paho's ordered delivery
			case got <- prefix + string(m.Payload()):
			default:
			}
		}); err != nil {
			t.Fatal(err) // subscribe waits for the SUBACK: no sleep needed
		}
	}

	order := `{"headerId":0,"timestamp":"2026-11-19T08:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1","orderId":"o1","orderUpdateId":0,
	 "nodes":[{"nodeId":"n1","sequenceId":0,"released":true,"nodePosition":{"x":0,"y":0,"mapId":"S1"},"actions":[]}],"edges":[]}`
	wcs.Publish("vda5050/v3/EMU/R1/order", 1, false, order).Wait()
	retrieve, _ := telegram.Encode(telegram.Telegram{ID: "t1", Type: telegram.Retrieve, Timestamp: time.Now(), JobID: "j1",
		ContainerID: "C1", PortID: "OUT1", From: &telegram.Position{Aisle: 1, Level: 1, Slot: 1, Depth: 1}})
	wcs.Publish(telegram.DownTopic("ASRS1"), 1, false, retrieve).Wait()

	go func() {
		for ctx.Err() == nil {
			_ = eng.Step(ctx, 100*time.Millisecond)
			time.Sleep(50 * time.Millisecond)
		}
	}()

	var sawOrder, sawAck, sawDone bool
	deadline := time.After(15 * time.Second)
	for !(sawOrder && sawAck && sawDone) {
		select {
		case m := <-got:
			switch {
			case strings.HasPrefix(m, "state:") && strings.Contains(m, `"orderId":"o1"`):
				sawOrder = true
			case strings.HasPrefix(m, "up:"):
				var tg map[string]any
				_ = json.Unmarshal([]byte(strings.TrimPrefix(m, "up:")), &tg)
				if tg["type"] == "ACK" && tg["ackTelegramId"] == "t1" && tg["accepted"] == true {
					sawAck = true
				}
				if tg["type"] == "JOB_DONE" && tg["jobId"] == "j1" {
					sawDone = true
				}
			}
		case <-deadline:
			t.Fatalf("order=%v ack=%v done=%v", sawOrder, sawAck, sawDone)
		}
	}
}

type cmdsProxy struct{ e **engine.Engine }

func (p cmdsProxy) HandleOrder(ctx context.Context, s string, o robot.Order) error {
	if *p.e == nil {
		return errNoEngine
	}
	return (*p.e).HandleOrder(ctx, s, o)
}
func (p cmdsProxy) HandleInstantActions(ctx context.Context, s string, as []robot.Action) error {
	if *p.e == nil {
		return errNoEngine
	}
	return (*p.e).HandleInstantActions(ctx, s, as)
}

func (p cmdsProxy) AnnounceRobot(ctx context.Context, s string) error {
	if *p.e == nil {
		return errNoEngine
	}
	return (*p.e).AnnounceRobot(ctx, s)
}

type asrsProxy struct{ e **engine.Engine }

func (p asrsProxy) SubmitJob(id string, j asrs.Job) error {
	if *p.e == nil {
		return errNoEngine
	}
	return (*p.e).SubmitJob(id, j)
}
func (p asrsProxy) CancelJob(id, job string) error {
	if *p.e == nil {
		return errNoEngine
	}
	return (*p.e).CancelJob(id, job)
}
func (p asrsProxy) UnitStates(ctx context.Context, id string) error {
	if *p.e == nil {
		return errNoEngine
	}
	return (*p.e).UnitStates(ctx, id)
}

var errNoEngine = errors.New("engine not ready")

func asrsPos(level, slot int) asrs.Position {
	return asrs.Position{Aisle: 1, Level: level, Slot: slot, Depth: 1}
}
