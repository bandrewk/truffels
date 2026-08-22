package main

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"truffels-api/internal/alerts"
	"truffels-api/internal/api"
	"truffels-api/internal/auth"
	"truffels-api/internal/bitcoin"
	"truffels-api/internal/catalog"
	composereconcile "truffels-api/internal/compose"
	"truffels-api/internal/config"
	"truffels-api/internal/docker"
	"truffels-api/internal/metrics"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
	"truffels-api/internal/syncstatus"
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

	// Agent client (Docker operations go through truffels-agent)
	agentURL := envOr("TRUFFELS_AGENT_URL", "http://truffels-agent:9090")
	compose := docker.NewComposeClient(agentURL)
	docker.NewAgentInspector(agentURL)
	slog.Info("agent configured", "url", agentURL)

	// Catalog client is constructed before the registry: the registry projects
	// installed catalog services, so it needs the catalog and the installation
	// list as sources.
	catalogClient := catalog.NewClient(agentURL)

	// Service registry: legacy templates plus a projection of installed
	// catalog services.
	registry := service.NewRegistry(cfg.ComposeRoot, cfg.DataRoot, cfg.GitHubRepo, catalogClient, st)
	if err := registry.Refresh(); err != nil {
		slog.Warn("initial registry refresh failed", "err", err)
	}

	// Ensure all services exist in DB
	for _, tmpl := range registry.All() {
		_ = st.EnsureService(tmpl.ID)
	}

	// Host metrics collector
	collector := metrics.NewCollector(cfg.HostProc, cfg.HostSys, cfg.DataRoot)

	// Alert engine (constructed early so we can pass it to the reconciler).
	// IMPORTANT: Start() is deferred until after Compose reconciliation so
	// the alert evaluator doesn't fire spurious warnings against services
	// that are still being reconciled. See v0.3.1-dev.14 startup-ordering fix.
	alertEngine := alerts.NewEngine(st, registry, collector, compose)
	// The engine refreshes chain-sync progress into this cache on its tick;
	// the Services handler reads it instead of probing nodes live.
	syncCache := syncstatus.NewCache()
	alertEngine.SetSyncCache(catalogClient, syncCache)
	defer alertEngine.Stop()

	// Update engine
	updateEngine := updates.NewEngine(st, registry, compose)
	updateEngine.Start()
	defer updateEngine.Stop()

	// Shutdown context, shared by the catalog-retry loop below and the graceful
	// shutdown handler further down. A cancelled context's Done channel is
	// closed (not consumed), so both consumers observe the signal — unlike a
	// shared signal channel, where whichever goroutine received it first would
	// starve the other.
	shutdownCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopSignals()

	// Arm the catalog guards and reconcile installed catalog services. The
	// agent may still be starting when the API boots (both restart together
	// on self-update), so retry until the catalog is served.
	go func() {
		for {
			ids, err := catalogClient.IDs()
			if err != nil {
				slog.Warn("catalog not yet available, retrying", "err", err)
				select {
				case <-time.After(10 * time.Second):
				case <-shutdownCtx.Done():
					slog.Info("shutting down during catalog retry")
					return
				}
				continue
			}
			updates.SetCatalogIDs(ids)
			slog.Info("catalog guards armed", "services", len(ids))

			// Reconcile installed catalog services on startup: re-apply each
			// idempotently so compose+config exist (covers a DB restore whose
			// files are missing). Running containers survive on their own via
			// restart:unless-stopped; this does not force anything to start.
			installs, err := st.ListCatalogInstallations()
			if err != nil {
				slog.Warn("list catalog installations failed", "err", err)
				return
			}
			for _, inst := range installs {
				if err := catalogClient.Apply(inst.CatalogID, inst.Params, true); err != nil {
					slog.Warn("catalog reconcile apply failed", "id", inst.CatalogID, "err", err)
				}
			}
			if err := registry.Refresh(); err != nil {
				slog.Warn("registry refresh after catalog reconcile failed", "err", err)
			}
			return
		}
	}()

	// Compose reconciliation — regenerate compose files from templates on startup.
	// Reconciler emits critical Alerts on compose-up failures so that a typo
	// in a service template doesn't silently break the service after self-update.
	reconciler := composereconcile.NewReconciler(registry, compose, st)
	if err := reconciler.Run(); err != nil {
		slog.Warn("compose reconciliation had errors", "err", err)
	}

	// Start alerts AFTER reconciliation so the first tick evaluates a
	// post-reconcile world (not a transient mid-restart state).
	alertEngine.Start()

	// Auth
	authenticator := auth.New(st)

	// Bitcoin RPC client
	btcRPC := initBitcoinRPC(cfg.SecretsRoot)

	// HTTP server
	srv := api.NewServer(registry, st, compose, collector, authenticator, btcRPC, updateEngine, catalogClient, syncCache, version)
	httpServer := &http.Server{
		Addr:    cfg.Listen,
		Handler: srv.Router(),
	}

	// Graceful shutdown — shutdownCtx was armed before the catalog-retry loop so
	// the same signal wakes that loop too (a closed Done channel fans out to
	// every waiter).
	go func() {
		<-shutdownCtx.Done()
		slog.Info("shutting down")
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
