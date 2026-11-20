// Command emulator runs the WCS equipment emulator (levels 1 and 0).
package main

import (
	"context"
	"errors"
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
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
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

	clock, robots, systems, nodes, err := cfg.World.Build(time.Now())
	if err != nil {
		return err
	}
	px := &proxy{}
	var ids []mqtt.RobotIdentity
	for _, r := range cfg.World.Robots {
		ids = append(ids, mqtt.RobotIdentity{Manufacturer: r.Manufacturer, SerialNumber: r.SerialNumber})
	}
	rg, err := mqtt.NewRobotGateway(cfg.MQTTURL, ids, px, log)
	if err != nil {
		return err
	}
	defer rg.Close()
	router := mqtt.AsrsRouter{}
	for _, s := range systems {
		g, err := mqtt.NewAsrsGateway(cfg.MQTTURL, s.ID(), px, time.Now, log)
		if err != nil {
			return err
		}
		defer g.Close()
		router[s.ID()] = g
	}
	eng := engine.New(clock, robots, systems, nodes, rg, router)
	px.set(eng)

	if err := startGateways(ctx, rg, router); err != nil {
		return err
	}
	if err := eng.Announce(ctx); err != nil {
		log.Warn("announce", "err", err)
	}

	ready := func() bool { return connected(rg, router) }
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: adminhttp.NewRouter(eng, ready), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
			stop()
		}
	}()

	tick := time.NewTicker(cfg.TickInterval)
	defer tick.Stop()
	step := simclock.Scale(cfg.TickInterval, cfg.SimSpeed)
	log.Info("emulator running", "robots", len(robots), "asrs", len(systems), "simSpeed", cfg.SimSpeed)
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return srv.Shutdown(shutdown)
		case <-tick.C:
			if err := eng.Step(ctx, step); err != nil {
				log.Warn("step", "err", err)
			}
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
