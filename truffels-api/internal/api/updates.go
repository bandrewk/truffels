package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"truffels-api/internal/model"
)

// UpdateCheckResponse adds the service's UpdateSource.Type to the on-the-wire
// UpdateCheck so the frontend can gate the version-selector dropdown on
// "dockerhub" sources only. SHA-based sources (github, bitbucket) and
// floating-tag (docker_digest) sources don't have meaningful pickable versions.
type UpdateCheckResponse struct {
	model.UpdateCheck
	SourceType string `json:"source_type,omitempty"`
}

func (s *Server) handleGetUpdates(w http.ResponseWriter, r *http.Request) {
	checks, err := s.store.GetAllUpdateChecks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make([]UpdateCheckResponse, 0, len(checks))
	for _, c := range checks {
		entry := UpdateCheckResponse{UpdateCheck: c}
		if tmpl, ok := s.registry.Get(c.ServiceID); ok && tmpl.UpdateSource != nil {
			entry.SourceType = string(tmpl.UpdateSource.Type)
		}
		resp = append(resp, entry)
	}
	writeJSON(w, http.StatusOK, resp)
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

	// Optional body: { "target_version": "31.0" }. Empty body / no field =
	// use auto-detected latest (unchanged dev.16 behavior).
	var body struct {
		TargetVersion string `json:"target_version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// If target_version is set and looks like a downgrade, gate on allow_downgrade.
	if body.TargetVersion != "" {
		if err := s.checkDowngradeAllowed(id, body.TargetVersion); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Run update in background
	target := body.TargetVersion
	go func() {
		if err := s.updateEngine.ApplyUpdateToVersion(id, target); err != nil {
			_ = s.store.LogAudit("update_failed", id, err.Error(), r.RemoteAddr)
		} else {
			_ = s.store.LogAudit("update_applied", id, "to "+target, r.RemoteAddr)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "update_started"})
}

// handleGetUpdateVersions returns the list of pickable versions for a service.
// Used by the Updates page's version selector dropdown.
func (s *Server) handleGetUpdateVersions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.registry.Get(id); !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	check, _ := s.store.GetLatestUpdateCheck(id)
	available, err := s.updateEngine.ListAvailableVersions(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := map[string]interface{}{
		"current":   "",
		"latest":    "",
		"available": available,
	}
	if check != nil {
		resp["current"] = check.CurrentVersion
		resp["latest"] = check.LatestVersion
	}
	if available == nil {
		resp["available"] = []string{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// checkDowngradeAllowed rejects target-version requests that go backwards
// in version unless the admin has explicitly enabled allow_downgrade in
// settings.
func (s *Server) checkDowngradeAllowed(serviceID, targetVersion string) error {
	check, _ := s.store.GetLatestUpdateCheck(serviceID)
	if check == nil {
		return nil
	}
	if compareSemverLike(targetVersion, check.CurrentVersion) >= 0 {
		return nil
	}
	if s.getSettingStr("allow_downgrade", "false") == "true" {
		return nil
	}
	return fmt.Errorf("downgrade from %s to %s is not allowed; enable allow_downgrade in Settings", check.CurrentVersion, targetVersion)
}

// compareSemverLike parses two version strings into []int and compares. Same
// algorithm as updates.ListDockerHubVersions, kept local to avoid an api->updates
// import cycle for what's effectively a small utility.
func compareSemverLike(a, b string) int {
	pa := parseSemverLike(a)
	pb := parseSemverLike(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(pa) {
			av = pa[i]
		}
		if i < len(pb) {
			bv = pb[i]
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

func parseSemverLike(s string) []int {
	s = strings.TrimPrefix(s, "v")
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '.' && (c < '0' || c > '9') {
			s = s[:i]
			break
		}
	}
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
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
	rawChecks, _ := s.store.GetAllUpdateChecks()
	pendingCount, _ := s.store.PendingUpdateCount()

	// dev.19: enrich checks with source_type — same shape as handleGetUpdates.
	// Without this, the frontend's `c.source_type === 'dockerhub'` gate is
	// always false (undefined !== "dockerhub"), so the version-selector
	// dropdown never renders for any service. dev.18 added source_type to
	// /updates but the UI reads /updates/status; this catches that miss.
	checks := make([]UpdateCheckResponse, 0, len(rawChecks))
	for _, c := range rawChecks {
		entry := UpdateCheckResponse{UpdateCheck: c}
		if tmpl, ok := s.registry.Get(c.ServiceID); ok && tmpl.UpdateSource != nil {
			entry.SourceType = string(tmpl.UpdateSource.Type)
		}
		checks = append(checks, entry)
	}

	updating := make(map[string]bool)
	sources := make(map[string]*model.UpdateSource)
	displayNames := make(map[string]string)
	type floatingService struct {
		ID             string `json:"id"`
		DisplayName    string `json:"display_name"`
		Image          string `json:"image"`
		CurrentVersion string `json:"current_version"`
		StartedAt      string `json:"started_at"`
	}
	var floating []floatingService
	// One batched inspect for all services (this endpoint is polled every 5s);
	// the floating-tag branch below reads the running image from this map
	// instead of calling the agent once per floating-tag service.
	byName := inspectAllContainers(s.registry.All())
	for _, tmpl := range s.registry.All() {
		if s.updateEngine.IsUpdating(tmpl.ID) {
			updating[tmpl.ID] = true
		}
		if tmpl.UpdateSource != nil {
			sources[tmpl.ID] = tmpl.UpdateSource
		}
		if tmpl.DisplayName != "" {
			displayNames[tmpl.ID] = tmpl.DisplayName
		}
		if tmpl.FloatingTag {
			fs := floatingService{ID: tmpl.ID, DisplayName: tmpl.DisplayName}
			// Read image and tag from the running container (from the batch).
			if len(tmpl.ContainerNames) > 0 {
				if cs, ok := byName[tmpl.ContainerNames[0]]; ok && cs.Image != "" {
					img := cs.Image
					// Strip digest
					if at := strings.Index(img, "@"); at >= 0 {
						img = img[:at]
					}
					fs.Image = img
					// Extract tag as version
					if colon := strings.LastIndex(img, ":"); colon >= 0 {
						fs.CurrentVersion = img[colon+1:]
					}
					fs.StartedAt = cs.StartedAt
				}
			}
			floating = append(floating, fs)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pending_count":     pendingCount,
		"checks":            checks,
		"updating":          updating,
		"sources":           sources,
		"floating_services": floating,
		"display_names":     displayNames,
	})
}
