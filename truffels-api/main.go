package main

import (
	"bufio"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"truffels-api/internal/alerts"
	"truffels-api/internal/api"
	"truffels-api/internal/auth"
	"truffels-api/internal/bitcoin"
	composereconcile "truffels-api/internal/compose"
	"truffels-api/internal/config"
	"truffels-api/internal/docker"
	"truffels-api/internal/metrics"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
	"truffels-api/internal/updates"
)

var version = "dev" // overridden via -ldflags "-X main.version=v0.2.0"

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
	registry := service.NewRegistry(cfg.ComposeRoot, cfg.GitHubRepo)

	// Ensure all services exist in DB
	for _, tmpl := range registry.All() {
		_ = st.EnsureService(tmpl.ID)
	}

	// Agent client (Docker operations go through truffels-agent)
	agentURL := envOr("TRUFFELS_AGENT_URL", "http://truffels-agent:9090")
	compose := docker.NewComposeClient(agentURL)
	docker.NewAgentInspector(agentURL)
	slog.Info("agent configured", "url", agentURL)

	// Host metrics collector
	collector := metrics.NewCollector(cfg.HostProc, cfg.HostSys, cfg.DataRoot)

	// Alert engine
	alertEngine := alerts.NewEngine(st, registry, collector, compose)
	alertEngine.Start()
	defer alertEngine.Stop()

	// Update engine
	updateEngine := updates.NewEngine(st, registry, compose)
	updateEngine.Start()
	defer updateEngine.Stop()

	// Compose reconciliation — regenerate compose files from templates on startup
	reconciler := composereconcile.NewReconciler(registry, compose)
	if err := reconciler.Run(); err != nil {
		slog.Warn("compose reconciliation had errors", "err", err)
	}

	// Auth
	authenticator := auth.New(st)

	// Bitcoin RPC client
	btcRPC := initBitcoinRPC(cfg.SecretsRoot)

	// HTTP server
	srv := api.NewServer(registry, st, compose, collector, authenticator, btcRPC, updateEngine, version)
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
		_ = httpServer.Close()
	}()

	slog.Info("starting truffels-api", "listen", cfg.Listen)
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func initBitcoinRPC(secretsRoot string) *bitcoin.Client {
	rpcHost := os.Getenv("BITCOIN_RPC_HOST")
	if rpcHost == "" {
		rpcHost = "truffels-bitcoind:8332"
	}

	envFile := secretsRoot + "/rpc.env"
	f, err := os.Open(envFile)
	if err != nil {
		slog.Warn("cannot open rpc.env, bitcoin stats disabled", "err", err)
		return nil
	}
	defer func() { _ = f.Close() }()

	var user, pass string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if k, v, ok := strings.Cut(line, "="); ok {
			switch k {
			case "RPC_USER":
				user = v
			case "RPC_PASSWORD":
				pass = v
			}
		}
	}

	if user == "" || pass == "" {
		slog.Warn("rpc.env missing credentials, bitcoin stats disabled")
		return nil
	}

	slog.Info("bitcoin RPC configured", "host", rpcHost)
	return bitcoin.NewClient(rpcHost, user, pass)
}
