package config

import (
	"testing"
	"time"
)

func TestExampleWorldBuilds(t *testing.T) {
	t.Setenv("WORLD_FILE", "../../config/world.example.yaml")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := c.World.Build(time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
}
