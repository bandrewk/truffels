package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"truffels-api/internal/model"
)

// settingsDefaults are the default values for all configurable settings.
var settingsDefaults = map[string]string{
	"restart_loop_count":      "5",
	"restart_loop_window_min": "10",
	"restart_loop_max_retries": "10",
	"dep_handling_mode":       "flag_only",
	"temp_warning":            "75",
	"temp_critical":           "80",
	"admission_disk_min_gb":          "10",
	"admission_temp_max":             "80",
	"update_check_interval_hours":    "24",
	"update_check_enabled":           "true",
	"update_channel":                 "stable",
	"services_show_memory":           "false",
	"services_show_ports":            "true",
	"update_keep_old_images":         "false",
	"trend_alert_enabled":            "true",
	"trend_alert_horizon_hours":      "6",
	"trend_alert_lookback_hours":     "6",
	"trend_alert_min_data_hours":     "2",
	"allow_downgrade":                "false",
	"dir_size_warning_mb":                     "700",
	"dir_size_critical_mb":                    "900",
	"dir_size_autoreclaim_enabled":            "true",
	"dir_size_autoreclaim_min_interval_hours": "24",
}

type settingsResponse struct {
	RestartLoopCount      int     `json:"restart_loop_count"`
	RestartLoopWindowMin  int     `json:"restart_loop_window_min"`
	RestartLoopMaxRetries int     `json:"restart_loop_max_retries"`
	DepHandlingMode       string  `json:"dep_handling_mode"`
	TempWarning           float64 `json:"temp_warning"`
	TempCritical          float64 `json:"temp_critical"`
	AdmissionDiskMinGB        float64 `json:"admission_disk_min_gb"`
	AdmissionTempMax          float64 `json:"admission_temp_max"`
	UpdateCheckIntervalHours int     `json:"update_check_interval_hours"`
	UpdateCheckEnabled       bool    `json:"update_check_enabled"`
	UpdateChannel            string  `json:"update_channel"`
	ServicesShowMemory       bool    `json:"services_show_memory"`
	ServicesShowPorts         bool    `json:"services_show_ports"`
	UpdateKeepOldImages      bool    `json:"update_keep_old_images"`
	TrendAlertEnabled        bool    `json:"trend_alert_enabled"`
	TrendAlertHorizonHours   int     `json:"trend_alert_horizon_hours"`
	TrendAlertLookbackHours  int     `json:"trend_alert_lookback_hours"`
	TrendAlertMinDataHours   int     `json:"trend_alert_min_data_hours"`
	AllowDowngrade           bool    `json:"allow_downgrade"`
	DirSizeWarningMB                   int  `json:"dir_size_warning_mb"`
	DirSizeCriticalMB                  int  `json:"dir_size_critical_mb"`
	DirSizeAutoreclaimEnabled          bool `json:"dir_size_autoreclaim_enabled"`
	DirSizeAutoreclaimMinIntervalHours int  `json:"dir_size_autoreclaim_min_interval_hours"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	resp := settingsResponse{
		RestartLoopCount:      s.getSettingInt("restart_loop_count", 5),
		RestartLoopWindowMin:  s.getSettingInt("restart_loop_window_min", 10),
		RestartLoopMaxRetries: s.getSettingInt("restart_loop_max_retries", 10),
		DepHandlingMode:       s.getSettingStr("dep_handling_mode", "flag_only"),
		TempWarning:           s.getSettingFloat("temp_warning", 75),
		TempCritical:          s.getSettingFloat("temp_critical", 80),
		AdmissionDiskMinGB:        s.getSettingFloat("admission_disk_min_gb", 10),
		AdmissionTempMax:          s.getSettingFloat("admission_temp_max", 80),
		UpdateCheckIntervalHours: s.getSettingInt("update_check_interval_hours", 24),
		UpdateCheckEnabled:       s.getSettingStr("update_check_enabled", "true") == "true",
		UpdateChannel:            s.getSettingStr("update_channel", "stable"),
		ServicesShowMemory:       s.getSettingStr("services_show_memory", "false") == "true",
		ServicesShowPorts:         s.getSettingStr("services_show_ports", "true") == "true",
		UpdateKeepOldImages:      s.getSettingStr("update_keep_old_images", "false") == "true",
		TrendAlertEnabled:        s.getSettingStr("trend_alert_enabled", "true") == "true",
		TrendAlertHorizonHours:   s.getSettingInt("trend_alert_horizon_hours", 6),
		TrendAlertLookbackHours:  s.getSettingInt("trend_alert_lookback_hours", 6),
		TrendAlertMinDataHours:   s.getSettingInt("trend_alert_min_data_hours", 2),
		AllowDowngrade:           s.getSettingStr("allow_downgrade", "false") == "true",
		DirSizeWarningMB:                   s.getSettingInt("dir_size_warning_mb", 700),
		DirSizeCriticalMB:                  s.getSettingInt("dir_size_critical_mb", 900),
		DirSizeAutoreclaimEnabled:          s.getSettingStr("dir_size_autoreclaim_enabled", "true") == "true",
		DirSizeAutoreclaimMinIntervalHours: s.getSettingInt("dir_size_autoreclaim_min_interval_hours", 24),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Validate the dir_size_* settings before writing anything. These drive
	// the unattended stop/clear/restart auto-reclaim behaviour, so a bad
	// value (e.g. dir_size_critical_mb = 0) has real consequences — see
	// task-3b brief. This pass must complete before the first SetSetting
	// call below for any of these keys.
	if msg := s.validateDirSizeSettings(body); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	for key, raw := range body {
		if _, ok := settingsDefaults[key]; !ok {
			writeError(w, http.StatusBadRequest, "unknown setting: "+key)
			return
		}

		var val string
		// Try as string first, then number, then boolean
		if err := json.Unmarshal(raw, &val); err != nil {
			var num float64
			if numErr := json.Unmarshal(raw, &num); numErr != nil {
				var b bool
				if boolErr := json.Unmarshal(raw, &b); boolErr != nil {
					writeError(w, http.StatusBadRequest, "invalid value for "+key)
					return
				}
				val = strconv.FormatBool(b)
			} else {
				if num == float64(int(num)) {
					val = strconv.Itoa(int(num))
				} else {
					val = strconv.FormatFloat(num, 'f', -1, 64)
				}
			}
		}

		if err := s.store.SetSetting(key, val); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	_ = s.store.LogAudit("settings_updated", "", "Settings updated", r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSystemShutdown(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	ok, err := s.auth.CheckPassword(body.Password)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	_ = s.store.LogAudit("system_shutdown", "", "System shutdown requested via UI", r.RemoteAddr)

	if err := s.compose.SystemAction("shutdown"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	ok, err := s.auth.CheckPassword(body.Password)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	_ = s.store.LogAudit("system_restart", "", "System restart requested via UI", r.RemoteAddr)

	if err := s.compose.SystemAction("restart"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Docker Prune ---

func (s *Server) handleDockerPrune(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	ok, err := s.auth.CheckPassword(body.Password)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	reclaimed, err := s.compose.DockerPrune()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = s.store.LogAudit("docker_prune", "", "Docker prune: "+reclaimed, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "reclaimed": reclaimed})
}

// --- Docker Build Cache Prune ---

func (s *Server) handleDockerPruneBuildCache(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	ok, err := s.auth.CheckPassword(body.Password)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	reclaimed, err := s.compose.DockerPruneBuildCache()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = s.store.LogAudit("docker_prune_buildcache", "", "Build cache prune: "+reclaimed, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "reclaimed": reclaimed})
}

// --- Clear Service Data ---

// handleClearServiceData empties a host data directory belonging to a managed
// service. The path must be declared in the service template's DataDirs with
// Clearable=true (e.g. mempool/cache). If RequiresStop, the service is stopped
// before the clear and started again after. Roll-forward on failure: if the
// clear step fails after stop, we still try to bring the service back up.
func (s *Server) handleClearServiceData(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password  string `json:"password"`
		ServiceID string `json:"service_id"`
		Path      string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ok, err := s.auth.CheckPassword(body.Password)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	if body.ServiceID == "" || body.Path == "" {
		writeError(w, http.StatusBadRequest, "service_id and path are required")
		return
	}

	// Look up the template and verify the path is declared clearable.
	tmpl, exists := s.registry.Get(body.ServiceID)
	if !exists {
		writeError(w, http.StatusBadRequest, "unknown service: "+body.ServiceID)
		return
	}
	var dd *model.DataDir
	for i := range tmpl.DataDirs {
		if tmpl.DataDirs[i].Path == body.Path {
			dd = &tmpl.DataDirs[i]
			break
		}
	}
	if dd == nil {
		writeError(w, http.StatusBadRequest, "path not registered for this service")
		return
	}
	if !dd.Clearable {
		writeError(w, http.StatusBadRequest, "path is not clearable")
		return
	}

	_ = s.store.LogAudit("service_data_clear", body.ServiceID, "Clear data dir: "+body.Path, r.RemoteAddr)

	// Stop first if required. If stop fails, abort entirely — don't try to
	// clear data of a still-running service.
	if dd.RequiresStop {
		if err := s.compose.Stop(body.ServiceID); err != nil {
			writeError(w, http.StatusInternalServerError, "stop failed: "+err.Error())
			return
		}
	}

	clearErr := s.compose.FsClearDir(body.Path, 1000, 1000, "0755")

	// Always try to bring service back up if we stopped it — even if clear failed.
	if dd.RequiresStop {
		if err := s.compose.Up(body.ServiceID); err != nil {
			if clearErr != nil {
				writeError(w, http.StatusInternalServerError,
					fmt.Sprintf("clear failed (%v) AND service restart failed (%v)", clearErr, err))
				return
			}
			writeError(w, http.StatusInternalServerError, "clear succeeded but service restart failed: "+err.Error())
			return
		}
	}

	if clearErr != nil {
		writeError(w, http.StatusInternalServerError, "clear failed: "+clearErr.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "path": body.Path})
}

// --- System Info ---

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.compose.SystemInfoGet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// --- System Journal ---

func (s *Server) handleSystemJournal(w http.ResponseWriter, r *http.Request) {
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 1000 {
			lines = n
		}
	}
	priority := r.URL.Query().Get("priority")
	unit := r.URL.Query().Get("unit")
	since := r.URL.Query().Get("since")
	boot := 0
	if v := r.URL.Query().Get("boot"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			boot = n
		}
	}

	// Validate priority
	validPriorities := map[string]bool{
		"": true, "emerg": true, "crit": true, "err": true,
		"warning": true, "info": true, "debug": true,
	}
	if !validPriorities[priority] {
		writeError(w, http.StatusBadRequest, "invalid priority")
		return
	}

	// Validate unit
	validUnits := map[string]bool{
		"": true, "docker": true, "kernel": true, "systemd": true,
		"nftables": true, "ssh": true,
	}
	if !validUnits[unit] {
		writeError(w, http.StatusBadRequest, "invalid unit")
		return
	}

	// Validate boot
	if boot > 0 {
		writeError(w, http.StatusBadRequest, "boot must be 0 or negative")
		return
	}

	logs, err := s.compose.SystemJournal(lines, priority, unit, since, boot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

// --- System Tuning ---

func (s *Server) handleSystemTuningGet(w http.ResponseWriter, r *http.Request) {
	info, err := s.compose.SystemTuningGet()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleSystemTuningSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		Value  string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Validate action
	validActions := map[string]bool{
		"set_persistent_journal": true,
		"set_swappiness":         true,
	}
	if !validActions[body.Action] {
		writeError(w, http.StatusBadRequest, "unknown action")
		return
	}

	// Validate value
	switch body.Action {
	case "set_persistent_journal":
		if body.Value != "true" && body.Value != "false" {
			writeError(w, http.StatusBadRequest, "value must be true or false")
			return
		}
	case "set_swappiness":
		n, err := strconv.Atoi(body.Value)
		if err != nil || n < 0 || n > 100 {
			writeError(w, http.StatusBadRequest, "swappiness must be 0-100")
			return
		}
	}

	if err := s.compose.SystemTuningSet(body.Action, body.Value); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = s.store.LogAudit("system_tuning", "", "Tuning: "+body.Action+"="+body.Value, r.RemoteAddr)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// validateDirSizeSettings validates the dir_size_* keys in an incoming PUT
// body, if present. It returns a non-empty error message (naming the
// offending key) if validation fails, or "" if the request is valid.
//
// The two thresholds are compared as "effective" values: the value in this
// request if the key is present, otherwise the value currently stored (or
// its default). A PUT may carry only one of the two thresholds, so
// validating the request body in isolation is not enough to catch e.g.
// dir_size_warning_mb raised above an unrelated, already-stored critical
// value.
func (s *Server) validateDirSizeSettings(body map[string]json.RawMessage) string {
	if raw, ok := body["dir_size_critical_mb"]; ok {
		n, valid := parseIntFromRaw(raw)
		if !valid || n < 1 {
			return "dir_size_critical_mb must be an integer >= 1"
		}
	}
	if raw, ok := body["dir_size_warning_mb"]; ok {
		n, valid := parseIntFromRaw(raw)
		if !valid || n < 1 {
			return "dir_size_warning_mb must be an integer >= 1"
		}
	}
	if raw, ok := body["dir_size_autoreclaim_min_interval_hours"]; ok {
		n, valid := parseIntFromRaw(raw)
		if !valid || n < 1 {
			return "dir_size_autoreclaim_min_interval_hours must be an integer >= 1"
		}
	}

	warningRaw, warningPresent := body["dir_size_warning_mb"]
	criticalRaw, criticalPresent := body["dir_size_critical_mb"]
	if warningPresent || criticalPresent {
		effectiveWarning := s.getSettingInt("dir_size_warning_mb", 700)
		if warningPresent {
			effectiveWarning, _ = parseIntFromRaw(warningRaw)
		}
		effectiveCritical := s.getSettingInt("dir_size_critical_mb", 900)
		if criticalPresent {
			effectiveCritical, _ = parseIntFromRaw(criticalRaw)
		}
		if effectiveWarning >= effectiveCritical {
			return "dir_size_warning_mb must be strictly less than dir_size_critical_mb"
		}
	}

	return ""
}

// parseIntFromRaw extracts an int from a raw JSON value that may be encoded
// as a JSON number or as a numeric string (handleUpdateSettings accepts
// both). Returns ok=false if the value can't be parsed as an integer.
func parseIntFromRaw(raw json.RawMessage) (n int, ok bool) {
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		return int(num), true
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		if v, err := strconv.Atoi(str); err == nil {
			return v, true
		}
	}
	return 0, false
}

func (s *Server) getSettingStr(key, def string) string {
	val, err := s.store.GetSetting(key)
	if err != nil || val == "" {
		return def
	}
	return val
}

func (s *Server) getSettingInt(key string, def int) int {
	val, err := s.store.GetSetting(key)
	if err != nil || val == "" {
		return def
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return def
	}
	return n
}

func (s *Server) getSettingFloat(key string, def float64) float64 {
	val, err := s.store.GetSetting(key)
	if err != nil || val == "" {
		return def
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return def
	}
	return f
}
