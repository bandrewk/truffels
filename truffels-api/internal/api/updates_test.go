package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"truffels-api/internal/auth"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
	"truffels-api/internal/updates"
)

// stubEngine is a minimal updates.Engine for testing handlers that call engine methods.
// We create a real Engine with nil compose client — TriggerCheck and IsUpdating don't need it.
func newTestServerWithEngine(t *testing.T) (*Server, *store.Store, *updates.Engine) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reg := service.NewRegistry("/srv/truffels/compose", "")
	a := auth.New(st)
	eng := updates.NewEngine(st, reg, nil)

	srv := NewServer(reg, st, nil, nil, a, nil, eng, "test")
	return srv, st, eng
}

// --- GET /updates ---

func TestGetUpdates_Empty(t *testing.T) {
	srv, _, _ := newTestServerWithEngine(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "GET", "/api/truffels/updates", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var checks []model.UpdateCheck
	if err := json.Unmarshal(w.Body.Bytes(), &checks); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("expected 0 checks, got %d", len(checks))
	}
}

func TestGetUpdates_WithData(t *testing.T) {
	srv, st, _ := newTestServerWithEngine(t)

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "29.0",
		LatestVersion:  "29.1",
		HasUpdate:      true,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "electrs",
		CurrentVersion: "v0.10.10",
		LatestVersion:  "v0.10.10",
		HasUpdate:      false,
	})

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "GET", "/api/truffels/updates", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var checks []model.UpdateCheck
	_ = json.Unmarshal(w.Body.Bytes(), &checks)
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}

	// Verify at least one has an update
	foundUpdate := false
	for _, c := range checks {
		if c.ServiceID == "bitcoind" && c.HasUpdate {
			foundUpdate = true
		}
	}
	if !foundUpdate {
		t.Fatal("expected bitcoind to have an update")
	}
}

// --- GET /updates/status ---

func TestUpdateStatus_Empty(t *testing.T) {
	srv, _, _ := newTestServerWithEngine(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "GET", "/api/truffels/updates/status", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)

	// pending_count should be 0
	if int(body["pending_count"].(float64)) != 0 {
		t.Fatalf("expected 0 pending, got %v", body["pending_count"])
	}

	// checks should be empty array
	checks, ok := body["checks"].([]interface{})
	if !ok {
		t.Fatalf("expected checks array, got %T", body["checks"])
	}
	if len(checks) != 0 {
		t.Fatalf("expected 0 checks, got %d", len(checks))
	}

	// updating should be empty map
	updating, ok := body["updating"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected updating map, got %T", body["updating"])
	}
	if len(updating) != 0 {
		t.Fatalf("expected 0 updating, got %d", len(updating))
	}

	// sources should contain entries for services with UpdateSource
	sources, ok := body["sources"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected sources map, got %T", body["sources"])
	}
	// bitcoind, electrs, mempool, ckpool, ckstats all have UpdateSource
	if len(sources) < 5 {
		t.Fatalf("expected at least 5 sources, got %d", len(sources))
	}
}

func TestUpdateStatus_WithPending(t *testing.T) {
	srv, st, _ := newTestServerWithEngine(t)

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "29.0",
		LatestVersion:  "29.1",
		HasUpdate:      true,
	})

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "GET", "/api/truffels/updates/status", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)

	if int(body["pending_count"].(float64)) != 1 {
		t.Fatalf("expected 1 pending, got %v", body["pending_count"])
	}

	checks := body["checks"].([]interface{})
	if len(checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(checks))
	}
}

// --- POST /updates/check ---

func TestCheckUpdates(t *testing.T) {
	srv, _, _ := newTestServerWithEngine(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "POST", "/api/truffels/updates/check", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "check_triggered" {
		t.Fatalf("expected check_triggered, got %q", body["status"])
	}
}

// --- POST /updates/apply/:id ---

func TestApplyUpdate_UnknownService(t *testing.T) {
	srv, _, _ := newTestServerWithEngine(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "POST", "/api/truffels/updates/apply/nonexistent", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestApplyUpdate_ValidService(t *testing.T) {
	srv, _, _ := newTestServerWithEngine(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "POST", "/api/truffels/updates/apply/bitcoind", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "update_started" {
		t.Fatalf("expected update_started, got %q", body["status"])
	}
}

// --- POST /updates/apply-all ---

func TestApplyAllUpdates_NoUpdates(t *testing.T) {
	srv, _, _ := newTestServerWithEngine(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "POST", "/api/truffels/updates/apply-all", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "no_updates" {
		t.Fatalf("expected no_updates, got %v", body["status"])
	}
	queued := body["queued"].([]interface{})
	if len(queued) != 0 {
		t.Fatalf("expected 0 queued, got %d", len(queued))
	}
}

func TestApplyAllUpdates_WithUpdates(t *testing.T) {
	srv, st, _ := newTestServerWithEngine(t)

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "29.0",
		LatestVersion:  "29.1",
		HasUpdate:      true,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "electrs",
		CurrentVersion: "v0.10.10",
		LatestVersion:  "v0.10.11",
		HasUpdate:      true,
	})
	// This one has an error — should NOT be queued
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool",
		CurrentVersion: "v3.2.0",
		LatestVersion:  "v3.3.0",
		HasUpdate:      true,
		Error:          "registry unreachable",
	})

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "POST", "/api/truffels/updates/apply-all", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "updates_started" {
		t.Fatalf("expected updates_started, got %v", body["status"])
	}

	queued := body["queued"].([]interface{})
	if len(queued) != 2 {
		t.Fatalf("expected 2 queued (mempool excluded due to error), got %d", len(queued))
	}

	// Verify mempool is not in queued list
	for _, q := range queued {
		if q.(string) == "mempool" {
			t.Fatal("mempool should not be queued due to error")
		}
	}
}

// --- GET /updates/logs ---

func TestUpdateLogs_Empty(t *testing.T) {
	srv, _, _ := newTestServerWithEngine(t)

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "GET", "/api/truffels/updates/logs", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var logs []model.UpdateLog
	_ = json.Unmarshal(w.Body.Bytes(), &logs)
	if len(logs) != 0 {
		t.Fatalf("expected 0 logs, got %d", len(logs))
	}
}

func TestUpdateLogs_WithData(t *testing.T) {
	srv, st, _ := newTestServerWithEngine(t)

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "bitcoind",
		FromVersion: "29.0",
		ToVersion:   "29.1",
		Status:      model.UpdateDone,
	})
	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "electrs",
		FromVersion: "v0.10.10",
		ToVersion:   "v0.10.11",
		Status:      model.UpdateFailed,
	})

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "GET", "/api/truffels/updates/logs", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var logs []model.UpdateLog
	_ = json.Unmarshal(w.Body.Bytes(), &logs)
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}
}

func TestUpdateLogs_FilterByService(t *testing.T) {
	srv, st, _ := newTestServerWithEngine(t)

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "bitcoind",
		FromVersion: "29.0",
		ToVersion:   "29.1",
		Status:      model.UpdateDone,
	})
	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "electrs",
		FromVersion: "v0.10.10",
		ToVersion:   "v0.10.11",
		Status:      model.UpdateFailed,
	})

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "GET", "/api/truffels/updates/logs?service=bitcoind", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var logs []model.UpdateLog
	_ = json.Unmarshal(w.Body.Bytes(), &logs)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log for bitcoind, got %d", len(logs))
	}
	if logs[0].ServiceID != "bitcoind" {
		t.Fatalf("expected bitcoind, got %q", logs[0].ServiceID)
	}
}

// --- Auth required ---

func TestUpdatesEndpoints_RequireAuth(t *testing.T) {
	srv, _, _ := newTestServerWithEngine(t)
	_ = srv.auth.SetPassword("testpassword")

	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/truffels/updates"},
		{"GET", "/api/truffels/updates/status"},
		{"POST", "/api/truffels/updates/check"},
		{"POST", "/api/truffels/updates/apply/bitcoind"},
		{"POST", "/api/truffels/updates/apply-all"},
		{"GET", "/api/truffels/updates/logs"},
	}

	for _, ep := range endpoints {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(ep.method, ep.path, nil)
		srv.Router().ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected 401, got %d", ep.method, ep.path, w.Code)
		}
	}
}
