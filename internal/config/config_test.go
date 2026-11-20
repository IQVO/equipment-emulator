package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	world := filepath.Join(dir, "world.yaml")
	_ = os.WriteFile(world, []byte("robots:\n  - manufacturer: EMU\n    serialNumber: R1\n    speed: 1\nasrs: []\n"), 0o600)
	t.Setenv("WORLD_FILE", world)
	t.Setenv("SIM_SPEED", "10")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.SimSpeed != 10 || c.TickInterval != 100*time.Millisecond || c.World.Robots[0].SerialNumber != "R1" || c.MQTTURL != "tcp://localhost:1883" {
		t.Fatalf("config = %+v", c)
	}
	t.Setenv("SIM_SPEED", "0")
	if _, err := Load(); err == nil {
		t.Fatal("zero speed must fail")
	}
}
