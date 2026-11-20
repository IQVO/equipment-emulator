package main

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

func TestProxyReturnsErrorsBeforeEngineIsSet(t *testing.T) {
	p := &proxy{}
	ctx := context.Background()
	errs := []error{
		p.HandleOrder(ctx, "R1", robot.Order{}),
		p.HandleInstantActions(ctx, "R1", nil),
		p.Announce(ctx),
		p.SubmitJob("A", asrs.Job{}),
		p.CancelJob("A", "J"),
		p.UnitStates(ctx, "A"),
	}
	for i, err := range errs {
		if !errors.Is(err, errNoEngine) {
			t.Fatalf("method %d: err = %v", i, err)
		}
	}
}
