// Command emulator runs the WCS equipment emulator (levels 1 and 0).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	adminhttp "github.com/IQVO/equipment-emulator/internal/adapters/inbound/http"
	"github.com/IQVO/equipment-emulator/internal/adapters/mqtt"
	"github.com/IQVO/equipment-emulator/internal/application/engine"
	"github.com/IQVO/equipment-emulator/internal/config"
	"github.com/IQVO/equipment-emulator/internal/domain/simclock"
)

func main() {
	log := newLogger(os.Stdout)
	slog.SetDefault(log) // the OTel SDK's own diagnostics go through slog.Default
	if err := run(log); err != nil {
		log.Error("emulator stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	tcfg := loadTelemetryEnv(os.Getenv)
	flush, err := startTelemetry(log, tcfg)
	if err != nil {
		return err
	}
	defer flush()

	// Simulated time starts at the process start; every published timestamp is
	// simulated time (at SIM_SPEED 1 it tracks wall time).
	clock, robots, systems, nodes, err := cfg.World.Build(time.Now())
	if err != nil {
		return err
	}
	px := &proxy{}
	var ids []mqtt.RobotIdentity
	for _, r := range cfg.World.Robots {
		ids = append(ids, mqtt.RobotIdentity{Manufacturer: r.Manufacturer, SerialNumber: r.SerialNumber})
	}
	rg, err := mqtt.NewRobotGateway(cfg.MQTTURL, ids, px, clock.Now, log)
	if err != nil {
		return err
	}
	defer rg.Close()
	router := mqtt.AsrsRouter{}
	for _, s := range systems {
		g, err := mqtt.NewAsrsGateway(cfg.MQTTURL, s.ID(), px, clock.Now, log)
		if err != nil {
			return err
		}
		defer g.Close()
		router[s.ID()] = g
	}
	eng := engine.New(clock, robots, systems, nodes, rg, router)
	px.set(eng)
	instrument(log, eng, clock, rg, router)

	if err := startGateways(ctx, rg, router); err != nil {
		return err
	}
	if err := eng.Announce(ctx); err != nil {
		log.Warn("announce", "err", err)
	}

	ready := func() bool { return connected(rg, router) }
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: adminhttp.NewRouter(eng, ready, log, tcfg.serviceName), ReadHeaderTimeout: 5 * time.Second}
	log.Info("emulator running", "robots", len(robots), "asrs", len(systems), "simSpeed", cfg.SimSpeed)
	step := simclock.Scale(cfg.TickInterval, cfg.SimSpeed)
	return serve(ctx, srv, cfg.TickInterval, func() {
		if err := eng.Step(ctx, step); err != nil {
			log.Warn("step", "err", err)
		}
	})
}

// serve runs srv and calls onTick every interval until ctx is done or the
// server fails. A listen failure is returned (so the process exits non-zero);
// a signal-driven stop returns the graceful shutdown's result.
func serve(ctx context.Context, srv *http.Server, interval time.Duration, onTick func()) error {
	serveErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case err := <-serveErr:
			return fmt.Errorf("http server: %w", err)
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(shutdown)
		case <-tick.C:
			onTick()
		}
	}
}

// connected reports whether the robot gateway and every AS/RS gateway hold a broker connection.
func connected(rg *mqtt.RobotGateway, router mqtt.AsrsRouter) bool {
	if !rg.Connected() {
		return false
	}
	for _, g := range router {
		if !g.Connected() {
			return false
		}
	}
	return true
}

func startGateways(ctx context.Context, rg *mqtt.RobotGateway, router mqtt.AsrsRouter) error {
	if err := rg.Start(ctx); err != nil {
		return err
	}
	for _, g := range router {
		if err := g.Start(ctx); err != nil {
			return err
		}
		go g.RunRetransmit(ctx)
	}
	return nil
}
