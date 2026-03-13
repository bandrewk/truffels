package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
)

func (s *Server) handleGetUpdates(w http.ResponseWriter, r *http.Request) {
	checks, err := s.store.GetAllUpdateChecks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if checks == nil {
		checks = []model.UpdateCheck{}
	}
	writeJSON(w, http.StatusOK, checks)
}

func (s *Server) handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	s.updateEngine.TriggerCheck()
	_ = s.store.LogAudit("update_check", "", "manual trigger", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{"status": "check_triggered"})
}

func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.registry.Get(id); !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	// Run update in background
	go func() {
		if err := s.updateEngine.ApplyUpdate(id); err != nil {
			_ = s.store.LogAudit("update_failed", id, err.Error(), r.RemoteAddr)
		} else {
			_ = s.store.LogAudit("update_applied", id, "", r.RemoteAddr)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "update_started"})
}

func (s *Server) handleApplyAllUpdates(w http.ResponseWriter, r *http.Request) {
	checks, err := s.store.GetAllUpdateChecks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var queued []string
	for _, c := range checks {
		if c.HasUpdate && c.Error == "" {
			queued = append(queued, c.ServiceID)
		}
	}

	if len(queued) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "no_updates",
			"queued": []string{},
		})
		return
	}

	// Apply updates sequentially in background
	go func() {
		for _, id := range queued {
			if err := s.updateEngine.ApplyUpdate(id); err != nil {
				_ = s.store.LogAudit("update_failed", id, err.Error(), "")
			} else {
				_ = s.store.LogAudit("update_applied", id, "", "")
			}
		}
	}()

	_ = s.store.LogAudit("update_all", "", "", r.RemoteAddr)
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status": "updates_started",
		"queued": queued,
	})
}

func (s *Server) handleUpdateLogs(w http.ResponseWriter, r *http.Request) {
	serviceID := r.URL.Query().Get("service")
	logs, err := s.store.GetUpdateLogs(serviceID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = []model.UpdateLog{}
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) handleUpdatePreflight(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	result, err := s.updateEngine.RunPreflight(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRollbackService(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.registry.Get(id); !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	go func() {
		if err := s.updateEngine.RollbackService(id); err != nil {
			_ = s.store.LogAudit("rollback_failed", id, err.Error(), r.RemoteAddr)
		} else {
			_ = s.store.LogAudit("rollback_applied", id, "", r.RemoteAddr)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "rollback_started"})
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	checks, _ := s.store.GetAllUpdateChecks()
	pendingCount, _ := s.store.PendingUpdateCount()
	if checks == nil {
		checks = []model.UpdateCheck{}
	}

	updating := make(map[string]bool)
	sources := make(map[string]*model.UpdateSource)
	type floatingService struct {
		ID             string `json:"id"`
		DisplayName    string `json:"display_name"`
		Image          string `json:"image"`
		CurrentVersion string `json:"current_version"`
		StartedAt      string `json:"started_at"`
	}
	var floating []floatingService
	for _, tmpl := range s.registry.All() {
		if s.updateEngine.IsUpdating(tmpl.ID) {
			updating[tmpl.ID] = true
		}
		if tmpl.UpdateSource != nil {
			sources[tmpl.ID] = tmpl.UpdateSource
		}
		if tmpl.FloatingTag {
			fs := floatingService{ID: tmpl.ID, DisplayName: tmpl.DisplayName}
			// Get image and tag from running container
			containers := docker.InspectContainers(tmpl.ContainerNames)
			if len(containers) > 0 && containers[0].Image != "" {
				img := containers[0].Image
				// Strip digest
				if at := strings.Index(img, "@"); at >= 0 {
					img = img[:at]
				}
				fs.Image = img
				// Extract tag as version
				if colon := strings.LastIndex(img, ":"); colon >= 0 {
					fs.CurrentVersion = img[colon+1:]
				}
				fs.StartedAt = containers[0].StartedAt
			}
			floating = append(floating, fs)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pending_count":    pendingCount,
		"checks":           checks,
		"updating":         updating,
		"sources":          sources,
		"floating_services": floating,
	})
}
