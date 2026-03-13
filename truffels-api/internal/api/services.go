package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
)

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	var services []model.ServiceInstance
	for _, tmpl := range s.registry.All() {
		containers := docker.InspectContainers(tmpl.ContainerNames)
		enabled, _ := s.store.IsServiceEnabled(tmpl.ID)
		svc := model.ServiceInstance{
			Template:   tmpl,
			State:      deriveState(containers),
			Enabled:    enabled,
			Containers: containers,
		}
		if !enabled && (svc.State == model.StateStopped || svc.State == model.StateUnknown) {
			svc.State = model.StateDisabled
		}
		svc.DependencyIssues = s.checkDependencyIssues(tmpl)
		s.enrichSyncInfo(&svc)
		services = append(services, svc)
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

	svc := model.ServiceInstance{
		Template:   tmpl,
		State:      deriveState(containers),
		Enabled:    enabled,
		Containers: containers,
	}
	if !enabled && (svc.State == model.StateStopped || svc.State == model.StateUnknown) {
		svc.State = model.StateDisabled
	}
	svc.DependencyIssues = s.checkDependencyIssues(tmpl)
	s.enrichSyncInfo(&svc)
	writeJSON(w, http.StatusOK, svc)
}

func (s *Server) enrichSyncInfo(svc *model.ServiceInstance) {
	if svc.State != model.StateRunning {
		return
	}
	switch svc.Template.ID {
	case "bitcoind":
		s.enrichBitcoindSync(svc)
	case "electrs":
		s.enrichElectrsSync(svc)
	}
}

func (s *Server) enrichBitcoindSync(svc *model.ServiceInstance) {
	if s.btcRPC == nil {
		return
	}
	info, err := s.btcRPC.GetBlockchainInfo()
	if err != nil {
		return
	}
	// Bitcoin Core reports verificationprogress < 0.9999 during IBD
	if info.VerificationProgress >= 0.9999 {
		return
	}
	pct := info.VerificationProgress * 100
	svc.SyncInfo = &model.SyncInfo{
		Syncing:  true,
		Progress: info.VerificationProgress,
		Detail:   fmt.Sprintf("%.2f%% (%s / %s blocks)", pct, formatInt(info.Blocks), formatInt(info.Headers)),
	}
}

func (s *Server) enrichElectrsSync(svc *model.ServiceInstance) {
	// Fetch electrs index height from Prometheus metrics
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://truffels-electrs:4224/")
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	indexHeight := parsePrometheusGauge(string(body), `electrs_index_height{type="tip"}`)
	if indexHeight == 0 {
		return
	}

	// Get bitcoind height for comparison
	if s.btcRPC == nil {
		return
	}
	bcInfo, err := s.btcRPC.GetBlockchainInfo()
	if err != nil || bcInfo.Blocks == 0 {
		return
	}

	if indexHeight >= bcInfo.Blocks {
		return // synced
	}

	progress := float64(indexHeight) / float64(bcInfo.Blocks)
	behind := bcInfo.Blocks - indexHeight
	pct := progress * 100
	svc.SyncInfo = &model.SyncInfo{
		Syncing:  true,
		Progress: progress,
		Detail:   fmt.Sprintf("%.1f%% (%s blocks behind)", pct, formatInt(behind)),
	}
}

func formatInt(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	// Insert commas
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
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

	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// pull-restart is allowed for all services (including ReadOnly)
	// start/stop/restart are blocked for ReadOnly services
	if req.Action != "pull-restart" && tmpl.ReadOnly {
		writeError(w, http.StatusForbidden, "this is an infrastructure service and cannot be managed")
		return
	}

	switch req.Action {
	case "start":
		// Check if service is enabled
		enabled, _ := s.store.IsServiceEnabled(id)
		if !enabled {
			writeError(w, http.StatusConflict, "service is disabled — enable it first")
			return
		}
		// Validate dependencies before starting
		isRunning := func(depID string) bool {
			dep, ok := s.registry.Get(depID)
			if !ok {
				return false
			}
			// Skip check for same-stack services — compose up will start them
			if dep.ComposeDir != "" && dep.ComposeDir == tmpl.ComposeDir {
				return true
			}
			containers := docker.InspectContainers(dep.ContainerNames)
			return deriveState(containers) == model.StateRunning
		}
		if err := s.registry.ValidateDependencies(id, isRunning); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		// Admission control: check system resources
		if s.collector != nil {
			host := s.collector.Collect()

			maxTemp := s.getSettingFloat("admission_temp_max", 80)
			if host.Temperature >= maxTemp {
				writeError(w, http.StatusConflict, fmt.Sprintf(
					"admission control: CPU temperature too high (%.1f°C, max %.0f°C)", host.Temperature, maxTemp))
				return
			}

			minDiskGB := s.getSettingFloat("admission_disk_min_gb", 10)
			for _, disk := range host.Disks {
				if disk.AvailGB < minDiskGB {
					writeError(w, http.StatusConflict, fmt.Sprintf(
						"admission control: insufficient disk space on %s (%.1f GB free, minimum %.0f GB)", disk.Path, disk.AvailGB, minDiskGB))
					return
				}
			}
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
		if err := s.compose.Stop(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

	case "restart":
		if err := s.compose.Restart(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

	case "pull-restart":
		// Pull latest images, check if anything changed, restart only if needed
		changed := false
		for _, cname := range tmpl.ContainerNames {
			info, err := s.compose.ImageInspect(cname)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "inspect failed: "+err.Error())
				return
			}
			imgRef := info.Image
			if atIdx := strings.Index(imgRef, "@"); atIdx >= 0 {
				imgRef = imgRef[:atIdx]
			}
			output, err := s.compose.Pull(imgRef)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "pull failed: "+err.Error())
				return
			}
			if !strings.Contains(output, "Image is up to date") {
				changed = true
			}
		}
		if !changed {
			_ = s.store.LogAudit("service_pull_restart", id, "already up to date", r.RemoteAddr)
			writeJSON(w, http.StatusOK, map[string]string{"status": "already_up_to_date", "action": "pull-restart"})
			return
		}
		// Use compose up (without down) — only recreates containers whose image changed
		if err := s.compose.Up(id); err != nil {
			writeError(w, http.StatusInternalServerError, "restart failed: "+err.Error())
			return
		}

	case "enable":
		_ = s.store.SetServiceEnabled(id, true)
		_ = s.store.LogAudit("service_enable", id, "", r.RemoteAddr)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "action": "enable"})
		return

	case "disable":
		// Stop the service first if running
		containers := docker.InspectContainers(tmpl.ContainerNames)
		if deriveState(containers) == model.StateRunning {
			_ = s.compose.Stop(id)
		}
		_ = s.store.SetServiceEnabled(id, false)
		_ = s.store.LogAudit("service_disable", id, "", r.RemoteAddr)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "action": "disable"})
		return

	default:
		writeError(w, http.StatusBadRequest, "action must be start, stop, restart, enable, disable, or pull-restart")
		return
	}

	_ = s.store.LogAudit("service_"+req.Action, id, "", r.RemoteAddr)
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
	since := r.URL.Query().Get("since")

	logs, err := s.compose.Logs(id, tail, since)
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
	_ = s.store.LogAudit("config_update", id, "", r.RemoteAddr)
	diff := simpleDiff(oldConfig, req.Config)
	_ = s.store.CreateConfigRevision(&model.ConfigRevision{
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

// checkDependencyIssues returns a list of unhealthy upstream dependencies.
func (s *Server) checkDependencyIssues(tmpl model.ServiceTemplate) []string {
	var issues []string
	for _, depID := range tmpl.Dependencies {
		depTmpl, ok := s.registry.Get(depID)
		if !ok {
			continue
		}
		for _, name := range depTmpl.ContainerNames {
			cs, err := docker.InspectContainer(name)
			if err != nil {
				continue
			}
			if cs.Health == "unhealthy" || cs.Status == "exited" || cs.Status == "restarting" || cs.Status == "not_found" {
				issues = append(issues, depID+" is "+cs.Status)
				break
			}
		}
	}
	return issues
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
