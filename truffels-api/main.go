package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"truffels-api/internal/alerts"
	"truffels-api/internal/api"
	"truffels-api/internal/auth"
	"truffels-api/internal/config"
	"truffels-api/internal/docker"
	"truffels-api/internal/metrics"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.Load()

	// SQLite store
	st, err := store.New(cfg.DBPath)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// Service registry
	registry := service.NewRegistry(cfg.ComposeRoot)

	// Ensure all services exist in DB
	for _, tmpl := range registry.All() {
		st.EnsureService(tmpl.ID)
	}

	// Docker compose client
	compose := docker.NewComposeClient()

	// Host metrics collector
	collector := metrics.NewCollector(cfg.HostProc, cfg.HostSys, cfg.DataRoot)

	// Alert engine
	alertEngine := alerts.NewEngine(st, registry, collector)
	alertEngine.Start()
	defer alertEngine.Stop()

	// Auth
	authenticator := auth.New(st)

	// HTTP server
	srv := api.NewServer(registry, st, compose, collector, authenticator)
	httpServer := &http.Server{
		Addr:    cfg.Listen,
		Handler: srv.Router(),
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		sig := <-sigCh
		slog.Info("shutting down", "signal", sig)
		httpServer.Close()
	}()

	slog.Info("starting truffels-api", "listen", cfg.Listen)
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
