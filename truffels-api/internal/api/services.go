package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
)

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	var services []model.ServiceInstance
	for _, tmpl := range s.registry.All() {
		containers := docker.InspectContainers(tmpl.ContainerNames)
		enabled, _ := s.store.IsServiceEnabled(tmpl.ID)
		services = append(services, model.ServiceInstance{
			Template:   tmpl,
			State:      deriveState(containers),
			Enabled:    enabled,
			Containers: containers,
		})
	}
	writeJSON(w, http.StatusOK, services)
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tmpl, ok := s.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	containers := docker.InspectContainers(tmpl.ContainerNames)
	enabled, _ := s.store.IsServiceEnabled(tmpl.ID)

	writeJSON(w, http.StatusOK, model.ServiceInstance{
		Template:   tmpl,
		State:      deriveState(containers),
		Enabled:    enabled,
		Containers: containers,
	})
}

type actionRequest struct {
	Action string `json:"action"`
}

func (s *Server) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tmpl, ok := s.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	if tmpl.ReadOnly {
		writeError(w, http.StatusForbidden, "this is an infrastructure service and cannot be managed")
		return
	}

	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	switch req.Action {
	case "start":
		// Validate dependencies before starting
		isRunning := func(depID string) bool {
			dep, ok := s.registry.Get(depID)
			if !ok {
				return false
			}
			containers := docker.InspectContainers(dep.ContainerNames)
			return deriveState(containers) == model.StateRunning
		}
		if err := s.registry.ValidateDependencies(id, isRunning); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if err := s.compose.Up(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

	case "stop":
		// Warn about dependents
		dependents := s.registry.Dependents(id)
		for _, depID := range dependents {
			dep, _ := s.registry.Get(depID)
			containers := docker.InspectContainers(dep.ContainerNames)
			if deriveState(containers) == model.StateRunning {
				writeError(w, http.StatusConflict, "cannot stop: "+depID+" depends on this service and is running")
				return
			}
		}
		if err := s.compose.Down(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

	case "restart":
		if err := s.compose.Restart(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

	default:
		writeError(w, http.StatusBadRequest, "action must be start, stop, or restart")
		return
	}

	s.store.LogAudit("service_"+req.Action, id, "", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "action": req.Action})
}

func (s *Server) handleServiceLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.registry.Get(id); !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	tail := 100
	if t := r.URL.Query().Get("tail"); t != "" {
		if v, err := strconv.Atoi(t); err == nil && v > 0 && v <= 1000 {
			tail = v
		}
	}

	logs, err := s.compose.Logs(id, tail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tmpl, ok := s.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	if tmpl.ConfigPath == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"config":    nil,
			"revisions": []model.ConfigRevision{},
			"message":   "this service uses environment-based configuration",
		})
		return
	}

	configFile := s.configRoot() + "/" + tmpl.ConfigPath
	data, err := os.ReadFile(configFile)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot read config: "+err.Error())
		return
	}

	revisions, _ := s.store.GetConfigRevisions(id, 20)
	if revisions == nil {
		revisions = []model.ConfigRevision{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"config":    string(data),
		"path":      tmpl.ConfigPath,
		"revisions": revisions,
	})
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tmpl, ok := s.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	if tmpl.ConfigPath == "" {
		writeError(w, http.StatusBadRequest, "this service does not support config file updates")
		return
	}

	var req struct {
		Config  string `json:"config"`
		Restart bool   `json:"restart"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	configFile := s.configRoot() + "/" + tmpl.ConfigPath

	// Read current config for diff
	oldData, _ := os.ReadFile(configFile)
	oldConfig := string(oldData)

	// Write new config
	if err := os.WriteFile(configFile, []byte(req.Config), 0640); err != nil {
		writeError(w, http.StatusInternalServerError, "cannot write config: "+err.Error())
		return
	}

	// Create revision + audit
	s.store.LogAudit("config_update", id, "", r.RemoteAddr)
	diff := simpleDiff(oldConfig, req.Config)
	s.store.CreateConfigRevision(&model.ConfigRevision{
		ServiceID:        id,
		Actor:            "admin",
		Diff:             diff,
		ConfigSnapshot:   req.Config,
		ValidationResult: "ok",
	})

	// Optionally restart
	if req.Restart {
		if err := s.compose.Restart(id); err != nil {
			writeJSON(w, http.StatusOK, map[string]string{
				"status":        "config_saved",
				"restart_error": err.Error(),
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) configRoot() string {
	if v := os.Getenv("TRUFFELS_CONFIG_ROOT"); v != "" {
		return v
	}
	return "/srv/truffels/config"
}

func simpleDiff(old, new string) string {
	if old == new {
		return "no changes"
	}
	if old == "" {
		return "initial config"
	}
	return "config updated"
}
