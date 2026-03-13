package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// settingsDefaults are the default values for all configurable settings.
var settingsDefaults = map[string]string{
	"restart_loop_count":      "5",
	"restart_loop_window_min": "10",
	"restart_loop_max_retries": "0",
	"dep_handling_mode":       "flag_only",
	"temp_warning":            "75",
	"temp_critical":           "80",
	"admission_disk_min_gb":   "10",
	"admission_temp_max":      "80",
}

type settingsResponse struct {
	RestartLoopCount      int     `json:"restart_loop_count"`
	RestartLoopWindowMin  int     `json:"restart_loop_window_min"`
	RestartLoopMaxRetries int     `json:"restart_loop_max_retries"`
	DepHandlingMode       string  `json:"dep_handling_mode"`
	TempWarning           float64 `json:"temp_warning"`
	TempCritical          float64 `json:"temp_critical"`
	AdmissionDiskMinGB    float64 `json:"admission_disk_min_gb"`
	AdmissionTempMax      float64 `json:"admission_temp_max"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	resp := settingsResponse{
		RestartLoopCount:      s.getSettingInt("restart_loop_count", 5),
		RestartLoopWindowMin:  s.getSettingInt("restart_loop_window_min", 10),
		RestartLoopMaxRetries: s.getSettingInt("restart_loop_max_retries", 0),
		DepHandlingMode:       s.getSettingStr("dep_handling_mode", "flag_only"),
		TempWarning:           s.getSettingFloat("temp_warning", 75),
		TempCritical:          s.getSettingFloat("temp_critical", 80),
		AdmissionDiskMinGB:    s.getSettingFloat("admission_disk_min_gb", 10),
		AdmissionTempMax:      s.getSettingFloat("admission_temp_max", 80),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	for key, raw := range body {
		if _, ok := settingsDefaults[key]; !ok {
			writeError(w, http.StatusBadRequest, "unknown setting: "+key)
			return
		}

		var val string
		// Try as string first, then number
		if err := json.Unmarshal(raw, &val); err != nil {
			var num float64
			if err := json.Unmarshal(raw, &num); err != nil {
				writeError(w, http.StatusBadRequest, "invalid value for "+key)
				return
			}
			if num == float64(int(num)) {
				val = strconv.Itoa(int(num))
			} else {
				val = strconv.FormatFloat(num, 'f', -1, 64)
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
