package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

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
}

func NewServer(reg *service.Registry, st *store.Store, comp *docker.ComposeClient, coll *metrics.Collector) *Server {
	return &Server{
		registry:  reg,
		store:     st,
		compose:   comp,
		collector: coll,
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(requestLogger)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type"},
		MaxAge:         300,
	}))

	r.Route("/api/truffels", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/dashboard", s.handleDashboard)
		r.Get("/host", s.handleHost)
		r.Get("/alerts", s.handleAlerts)

		r.Get("/services", s.handleListServices)
		r.Get("/services/{id}", s.handleGetService)
		r.Post("/services/{id}/action", s.handleServiceAction)
		r.Get("/services/{id}/logs", s.handleServiceLogs)
		r.Get("/services/{id}/config", s.handleGetConfig)
		r.Post("/services/{id}/config", s.handleUpdateConfig)
	})

	return r
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
