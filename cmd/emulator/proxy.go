package main

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/domain/asrs"
	"github.com/IQVO/equipment-emulator/internal/domain/robot"
)

var errNoEngine = errors.New("engine not ready")

// proxy lets the gateways be built before the engine they call into. Every
// method answers errNoEngine until the engine is set.
type proxy struct{ e atomic.Pointer[engine.Engine] }

func (p *proxy) set(e *engine.Engine) { p.e.Store(e) }

func (p *proxy) HandleOrder(ctx context.Context, s string, o robot.Order) error {
	e := p.e.Load()
	if e == nil {
		return errNoEngine
	}
	return e.HandleOrder(ctx, s, o)
}

func (p *proxy) HandleInstantActions(ctx context.Context, s string, as []robot.Action) error {
	e := p.e.Load()
	if e == nil {
		return errNoEngine
	}
	return e.HandleInstantActions(ctx, s, as)
}

func (p *proxy) AnnounceRobot(ctx context.Context, serial string) error {
	e := p.e.Load()
	if e == nil {
		return errNoEngine
	}
	return e.AnnounceRobot(ctx, serial)
}

func (p *proxy) SubmitJob(id string, j asrs.Job) error {
	e := p.e.Load()
	if e == nil {
		return errNoEngine
	}
	return e.SubmitJob(id, j)
}

func (p *proxy) CancelJob(id, job string) error {
	e := p.e.Load()
	if e == nil {
		return errNoEngine
	}
	return e.CancelJob(id, job)
}

func (p *proxy) UnitStates(ctx context.Context, id string) error {
	e := p.e.Load()
	if e == nil {
		return errNoEngine
	}
	return e.UnitStates(ctx, id)
}
