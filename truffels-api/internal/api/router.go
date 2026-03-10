package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"truffels-api/internal/auth"
	"truffels-api/internal/bitcoin"
	"truffels-api/internal/docker"
	"truffels-api/internal/metrics"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

type Server struct {
	registry  *service.Registry
	store     *store.Store
	compose   *docker.ComposeClient
	collector *metrics.Collector
	auth      *auth.Auth
	btcRPC    *bitcoin.Client
}

func NewServer(reg *service.Registry, st *store.Store, comp *docker.ComposeClient, coll *metrics.Collector, a *auth.Auth, btc *bitcoin.Client) *Server {
	return &Server{
		registry:  reg,
		store:     st,
		compose:   comp,
		collector: coll,
		auth:      a,
		btcRPC:    btc,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(requestLogger)

	r.Route("/api/truffels", func(r chi.Router) {
		// Public endpoints (no auth required)
		r.Get("/health", s.handleHealth)
		r.Get("/auth/status", s.handleAuthStatus)
		r.Post("/auth/login", s.handleAuthLogin)
		r.Post("/auth/setup", s.handleAuthSetup)
		r.Post("/auth/logout", s.handleAuthLogout)

		// Protected endpoints
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)

			r.Get("/dashboard", s.handleDashboard)
			r.Get("/host", s.handleHost)
			r.Get("/alerts", s.handleAlerts)
			r.Get("/audit", s.handleAuditLog)

			r.Post("/backup/export", s.handleBackupExport)
			r.Get("/backup/list", s.handleBackupList)
			r.Get("/backup/download", s.handleBackupDownload)

			r.Get("/services", s.handleListServices)
			r.Get("/services/bitcoind/stats", s.handleBitcoindStats)
			r.Get("/services/ckpool/stats", s.handleCkpoolStats)
			r.Get("/services/electrs/stats", s.handleElectrsStats)
			r.Get("/services/{id}", s.handleGetService)
			r.Post("/services/{id}/action", s.handleServiceAction)
			r.Get("/services/{id}/logs", s.handleServiceLogs)
			r.Get("/services/{id}/config", s.handleGetConfig)
			r.Post("/services/{id}/config", s.handleUpdateConfig)
		})
	})

	return r
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setup, _ := s.auth.IsSetup()
		if !setup {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "setup_required",
			})
			return
		}
		if !s.auth.ValidateSession(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "unauthorized",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
