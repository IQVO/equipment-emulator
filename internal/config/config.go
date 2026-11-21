// Package config reads the emulator's environment and world file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/IQVO/equipment-emulator/internal/application/engine"
)

type Config struct {
	MQTTURL      string
	HTTPAddr     string
	SimSpeed     float64
	TickInterval time.Duration
	World        engine.World
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() (Config, error) {
	c := Config{MQTTURL: env("MQTT_URL", "tcp://localhost:1883"), HTTPAddr: env("HTTP_ADDR", ":8080")}
	var err error
	if c.SimSpeed, err = strconv.ParseFloat(env("SIM_SPEED", "1"), 64); err != nil || c.SimSpeed <= 0 {
		return Config{}, fmt.Errorf("SIM_SPEED must be a positive number")
	}
	if c.TickInterval, err = time.ParseDuration(env("TICK_INTERVAL", "100ms")); err != nil || c.TickInterval <= 0 {
		return Config{}, fmt.Errorf("TICK_INTERVAL must be a positive duration")
	}
	path := os.Getenv("WORLD_FILE")
	if path == "" {
		return Config{}, fmt.Errorf("WORLD_FILE is required")
	}
	b, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // the world file path is operator-supplied configuration
	if err != nil {
		return Config{}, err
	}
	if err := yaml.Unmarshal(b, &c.World); err != nil {
		return Config{}, fmt.Errorf("WORLD_FILE: %w", err)
	}
	return c, nil
}
