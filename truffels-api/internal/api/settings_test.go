package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- dir_size_* settings (Task 3b: expose auto-reclaim settings) ---

func TestSettingsGet_DirSizeDefaults(t *testing.T) {
	srv, _ := newTestServer(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "GET", "/api/truffels/settings", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp settingsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.DirSizeWarningMB != 700 {
		t.Errorf("expected dir_size_warning_mb=700, got %d", resp.DirSizeWarningMB)
	}
	if resp.DirSizeCriticalMB != 900 {
		t.Errorf("expected dir_size_critical_mb=900, got %d", resp.DirSizeCriticalMB)
	}
	if resp.DirSizeAutoreclaimEnabled != true {
		t.Errorf("expected dir_size_autoreclaim_enabled=true, got %v", resp.DirSizeAutoreclaimEnabled)
	}
	if resp.DirSizeAutoreclaimMinIntervalHours != 24 {
		t.Errorf("expected dir_size_autoreclaim_min_interval_hours=24, got %d", resp.DirSizeAutoreclaimMinIntervalHours)
	}
}

func TestSettingsPut_DirSizeValidValues_Roundtrip(t *testing.T) {
	srv, _ := newTestServer(t)

	body := `{
		"dir_size_warning_mb": 500,
		"dir_size_critical_mb": 800,
		"dir_size_autoreclaim_enabled": true,
		"dir_size_autoreclaim_min_interval_hours": 12
	}`
	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "PUT", "/api/truffels/settings", body)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w2 := httptest.NewRecorder()
	req2 := authenticatedRequest(t, srv, "GET", "/api/truffels/settings", "")
	srv.Router().ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var resp settingsResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.DirSizeWarningMB != 500 {
		t.Errorf("expected dir_size_warning_mb=500, got %d", resp.DirSizeWarningMB)
	}
	if resp.DirSizeCriticalMB != 800 {
		t.Errorf("expected dir_size_critical_mb=800, got %d", resp.DirSizeCriticalMB)
	}
	if resp.DirSizeAutoreclaimEnabled != true {
		t.Errorf("expected dir_size_autoreclaim_enabled=true, got %v", resp.DirSizeAutoreclaimEnabled)
	}
	if resp.DirSizeAutoreclaimMinIntervalHours != 12 {
		t.Errorf("expected dir_size_autoreclaim_min_interval_hours=12, got %d", resp.DirSizeAutoreclaimMinIntervalHours)
	}
}

func TestSettingsPut_DirSizeCriticalZero_Rejected(t *testing.T) {
	srv, _ := newTestServer(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "PUT", "/api/truffels/settings",
		`{"dir_size_critical_mb": 0}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var errBody map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &errBody)
	if !strings.Contains(errBody["error"], "dir_size_critical_mb") {
		t.Errorf("expected error to name dir_size_critical_mb, got %q", errBody["error"])
	}
}

func TestSettingsPut_WarningAboveStoredCritical_Rejected(t *testing.T) {
	srv, _ := newTestServer(t)

	// Body carries only the warning threshold. The stored (default) critical
	// is 900, so the effective comparison must use the stored value, not
	// just what's in this request body.
	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "PUT", "/api/truffels/settings",
		`{"dir_size_warning_mb": 2000}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var errBody map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &errBody)
	if !strings.Contains(errBody["error"], "dir_size_warning_mb") {
		t.Errorf("expected error to name dir_size_warning_mb, got %q", errBody["error"])
	}

	// And the bad value must not have been persisted.
	w2 := httptest.NewRecorder()
	req2 := authenticatedRequest(t, srv, "GET", "/api/truffels/settings", "")
	srv.Router().ServeHTTP(w2, req2)
	var resp settingsResponse
	_ = json.Unmarshal(w2.Body.Bytes(), &resp)
	if resp.DirSizeWarningMB != 700 {
		t.Errorf("rejected PUT must not persist: expected dir_size_warning_mb still 700, got %d", resp.DirSizeWarningMB)
	}
}

func TestSettingsPut_AutoreclaimKillSwitch(t *testing.T) {
	srv, _ := newTestServer(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "PUT", "/api/truffels/settings",
		`{"dir_size_autoreclaim_enabled": false}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	w2 := httptest.NewRecorder()
	req2 := authenticatedRequest(t, srv, "GET", "/api/truffels/settings", "")
	srv.Router().ServeHTTP(w2, req2)

	var resp settingsResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.DirSizeAutoreclaimEnabled != false {
		t.Fatalf("expected dir_size_autoreclaim_enabled=false after kill switch, got %v", resp.DirSizeAutoreclaimEnabled)
	}
}
