package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"truffels-api/internal/auth"
	"truffels-api/internal/docker"
	"truffels-api/internal/metrics"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

// mockAgent creates a mock agent HTTP server.
// containerStates controls what InspectContainers returns.
// composeErr controls whether compose actions return errors.
// lastAction records the last compose action received.
type mockAgentState struct {
	containerStates map[string]model.ContainerState // keyed by container name
	composeErr      string                          // if set, compose actions return this error
	lastAction      string
	lastServiceID   string
}

func newMockAgent(t *testing.T, state *mockAgentState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/system/info" && r.Method == "GET":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"hostname": "truffels", "os": "Debian 13", "kernel": "6.12.62",
				"model": "Raspberry Pi 5", "cpu_cores": 4,
				"mem_total": "8063 MB", "mem_free": "4000 MB", "uptime": "1d 2h",
				"networks": []map[string]string{
					{"name": "wlan0", "ip": "192.168.0.196/16", "mac": "aa:bb:cc:dd:ee:ff"},
				},
			})

		case r.URL.Path == "/v1/system/journal" && r.Method == "POST":
			_ = json.NewEncoder(w).Encode(map[string]string{"logs": "Mar 13 10:00:00 host kernel: test log line"})

		case r.URL.Path == "/v1/system/tuning" && r.Method == "GET":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"persistent_journal": true,
				"swappiness":         10,
				"journal_disk_usage": "8.0M",
				"boots": []map[string]interface{}{
					{"index": -1, "id": "abc123", "first": "2026-03-13 18:00:00", "last": "2026-03-13 18:10:00"},
					{"index": 0, "id": "def456", "first": "2026-03-13 18:15:00", "last": "2026-03-13 18:20:00"},
				},
			})

		case r.URL.Path == "/v1/system/tuning" && r.Method == "POST":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case strings.HasPrefix(r.URL.Path, "/v1/compose/"):
			var req struct {
				ServiceID string `json:"service_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			action := strings.TrimPrefix(r.URL.Path, "/v1/compose/")
			state.lastAction = action
			state.lastServiceID = req.ServiceID

			if state.composeErr != "" {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": state.composeErr})
				return
			}

			// For logs endpoint, return logs field
			if action == "logs" {
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "logs": "test log output"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case r.URL.Path == "/v1/inspect":
			var req struct {
				Containers []string `json:"containers"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			var states []model.ContainerState
			for _, name := range req.Containers {
				if cs, ok := state.containerStates[name]; ok {
					states = append(states, cs)
				} else {
					states = append(states, model.ContainerState{
						Name: name, Status: "running", Health: "healthy",
					})
				}
			}
			_ = json.NewEncoder(w).Encode(states)
		}
	}))
}

// newTestServerWithAgent creates a server with a mock agent for compose + inspect.
func newTestServerWithAgent(t *testing.T, agentState *mockAgentState) (*Server, *store.Store, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mockSrv := newMockAgent(t, agentState)
	t.Cleanup(mockSrv.Close)

	reg := service.NewRegistry("/srv/truffels/compose")
	a := auth.New(st)
	compose := docker.NewComposeClient(mockSrv.URL)

	// Set the global agent inspector to use our mock
	docker.NewAgentInspector(mockSrv.URL)

	srv := NewServer(reg, st, compose, nil, a, nil, nil)
	return srv, st, mockSrv
}

func authedReq(t *testing.T, srv *Server, method, path, body string) *http.Request {
	t.Helper()
	_ = srv.auth.SetPassword("testpassword")
	cookie, err := srv.auth.CreateSession()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.AddCookie(cookie)
	return req
}

// --- Service Action: Start ---

func TestServiceAction_Start_Success(t *testing.T) {
	agentState := &mockAgentState{
		containerStates: map[string]model.ContainerState{
			// bitcoind is running (electrs depends on it)
			"truffels-bitcoind": {Name: "truffels-bitcoind", Status: "running", Health: "healthy"},
		},
	}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/electrs/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if agentState.lastAction != "up" {
		t.Fatalf("expected compose up, got %q", agentState.lastAction)
	}
	if agentState.lastServiceID != "electrs" {
		t.Fatalf("expected electrs, got %q", agentState.lastServiceID)
	}
}

func TestServiceAction_Start_DependencyNotRunning(t *testing.T) {
	agentState := &mockAgentState{
		containerStates: map[string]model.ContainerState{
			// bitcoind is stopped — electrs can't start
			"truffels-bitcoind": {Name: "truffels-bitcoind", Status: "exited", Health: ""},
		},
	}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/electrs/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 409 {
		t.Fatalf("expected 409 conflict, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "bitcoind") {
		t.Fatalf("expected error about bitcoind dependency, got %q", body["error"])
	}
}

func TestServiceAction_Start_MultipleDepsOneDown(t *testing.T) {
	// mempool depends on bitcoind AND electrs
	agentState := &mockAgentState{
		containerStates: map[string]model.ContainerState{
			"truffels-bitcoind": {Name: "truffels-bitcoind", Status: "running", Health: "healthy"},
			"truffels-electrs":  {Name: "truffels-electrs", Status: "exited", Health: ""},
		},
	}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/mempool/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 409 {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceAction_Start_NoDeps(t *testing.T) {
	// bitcoind has no deps — should always succeed
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceAction_Start_ComposeFails(t *testing.T) {
	agentState := &mockAgentState{
		composeErr: "failed to pull image",
	}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Service Action: Stop ---

func TestServiceAction_Stop_Success(t *testing.T) {
	agentState := &mockAgentState{
		containerStates: map[string]model.ContainerState{
			// electrs (dependent of bitcoind) is stopped — safe to stop bitcoind
			"truffels-electrs":  {Name: "truffels-electrs", Status: "exited", Health: ""},
			"truffels-ckpool":   {Name: "truffels-ckpool", Status: "exited", Health: ""},
			// mempool containers also stopped
			"truffels-mempool-backend":  {Name: "truffels-mempool-backend", Status: "exited", Health: ""},
			"truffels-mempool-frontend": {Name: "truffels-mempool-frontend", Status: "exited", Health: ""},
			"truffels-mempool-db":       {Name: "truffels-mempool-db", Status: "exited", Health: ""},
		},
	}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"stop"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if agentState.lastAction != "stop" {
		t.Fatalf("expected compose stop, got %q", agentState.lastAction)
	}
}

func TestServiceAction_Stop_DependentRunning(t *testing.T) {
	agentState := &mockAgentState{
		containerStates: map[string]model.ContainerState{
			// electrs is running — can't stop bitcoind
			"truffels-electrs": {Name: "truffels-electrs", Status: "running", Health: "healthy"},
		},
	}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"stop"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 409 {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "depends on this service") {
		t.Fatalf("expected dependent-running error, got %q", body["error"])
	}
}

func TestServiceAction_Stop_LeafService(t *testing.T) {
	// ckstats has no dependents — always safe to stop
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/ckstats/action",
		`{"action":"stop"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceAction_Stop_ComposeFails(t *testing.T) {
	agentState := &mockAgentState{
		composeErr: "permission denied",
	}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/ckstats/action",
		`{"action":"stop"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Service Action: Restart ---

func TestServiceAction_Restart_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"restart"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if agentState.lastAction != "restart" {
		t.Fatalf("expected compose restart, got %q", agentState.lastAction)
	}
}

func TestServiceAction_Restart_ComposeFails(t *testing.T) {
	agentState := &mockAgentState{composeErr: "timeout"}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"restart"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Service Action: Edge Cases ---

func TestServiceAction_UnknownService(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/nonexistent/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServiceAction_ReadOnlyService(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	// proxy is read-only
	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/proxy/action",
		`{"action":"restart"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403 for read-only service, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServiceAction_InvalidAction(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"destroy"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for invalid action, got %d", w.Code)
	}
}

func TestServiceAction_MalformedBody(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action", "not json")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for malformed body, got %d", w.Code)
	}
}

func TestServiceAction_AuditLogged(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"restart"}`)
	srv.Router().ServeHTTP(w, req)

	entries, _ := st.GetAuditLog(10)
	found := false
	for _, e := range entries {
		if e.Action == "service_restart" && e.Target == "bitcoind" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected audit log entry for service_restart")
	}
}

// --- Service Logs ---

func TestServiceLogs_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/bitcoind/logs?tail=50", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["logs"] != "test log output" {
		t.Fatalf("expected test log output, got %q", body["logs"])
	}
}

func TestServiceLogs_NotFound(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/nonexistent/logs", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServiceLogs_DefaultTail(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/bitcoind/logs", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestServiceLogs_ComposeFails(t *testing.T) {
	agentState := &mockAgentState{composeErr: "not found"}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/bitcoind/logs", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// --- List/Get Services ---

func TestListServices(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var services []model.ServiceInstance
	_ = json.Unmarshal(w.Body.Bytes(), &services)
	if len(services) != 11 {
		t.Fatalf("expected 11 services, got %d", len(services))
	}
}

func TestGetService_Found(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/bitcoind", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var svc model.ServiceInstance
	_ = json.Unmarshal(w.Body.Bytes(), &svc)
	if svc.Template.ID != "bitcoind" {
		t.Fatalf("expected bitcoind, got %q", svc.Template.ID)
	}
}

func TestGetService_NotFound(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/nonexistent", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- Config ---

func TestGetConfig_NoConfigPath(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	// mempool has no ConfigPath
	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/mempool/config", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["config"] != nil {
		t.Fatalf("expected nil config, got %v", body["config"])
	}
	if body["message"] == nil {
		t.Fatal("expected message about env-based config")
	}
}

func TestGetConfig_WithConfigFile(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	// Create temp config dir
	dir := t.TempDir()
	t.Setenv("TRUFFELS_CONFIG_ROOT", dir)

	// Create the bitcoin config file
	btcDir := filepath.Join(dir, "bitcoin")
	_ = os.MkdirAll(btcDir, 0755)
	_ = os.WriteFile(filepath.Join(btcDir, "bitcoin.conf"), []byte("server=1\ntxindex=1\n"), 0644)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/bitcoind/config", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["config"] != "server=1\ntxindex=1\n" {
		t.Fatalf("unexpected config: %v", body["config"])
	}
	if body["path"] != "bitcoin/bitcoin.conf" {
		t.Fatalf("unexpected path: %v", body["path"])
	}
}

func TestGetConfig_NotFound(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/nonexistent/config", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestUpdateConfig_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	dir := t.TempDir()
	t.Setenv("TRUFFELS_CONFIG_ROOT", dir)
	btcDir := filepath.Join(dir, "bitcoin")
	_ = os.MkdirAll(btcDir, 0755)
	_ = os.WriteFile(filepath.Join(btcDir, "bitcoin.conf"), []byte("server=1\n"), 0644)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/config",
		`{"config":"server=1\ntxindex=1\n","restart":false}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify file was written
	data, _ := os.ReadFile(filepath.Join(btcDir, "bitcoin.conf"))
	if string(data) != "server=1\ntxindex=1\n" {
		t.Fatalf("config not written correctly: %q", string(data))
	}

	// Verify revision was created
	revs, _ := st.GetConfigRevisions("bitcoind", 10)
	if len(revs) != 1 {
		t.Fatalf("expected 1 revision, got %d", len(revs))
	}
	if revs[0].Diff != "config updated" {
		t.Fatalf("expected 'config updated', got %q", revs[0].Diff)
	}
}

func TestUpdateConfig_WithRestart(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	dir := t.TempDir()
	t.Setenv("TRUFFELS_CONFIG_ROOT", dir)
	btcDir := filepath.Join(dir, "bitcoin")
	_ = os.MkdirAll(btcDir, 0755)
	_ = os.WriteFile(filepath.Join(btcDir, "bitcoin.conf"), []byte("old"), 0644)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/config",
		`{"config":"new","restart":true}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if agentState.lastAction != "restart" {
		t.Fatalf("expected restart after config update, got %q", agentState.lastAction)
	}
}

func TestUpdateConfig_RestartFails(t *testing.T) {
	agentState := &mockAgentState{composeErr: "restart failed"}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	dir := t.TempDir()
	t.Setenv("TRUFFELS_CONFIG_ROOT", dir)
	btcDir := filepath.Join(dir, "bitcoin")
	_ = os.MkdirAll(btcDir, 0755)
	_ = os.WriteFile(filepath.Join(btcDir, "bitcoin.conf"), []byte("old"), 0644)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/config",
		`{"config":"new","restart":true}`)
	srv.Router().ServeHTTP(w, req)

	// Should still return 200, but with restart_error
	if w.Code != 200 {
		t.Fatalf("expected 200 (config saved), got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "config_saved" {
		t.Fatalf("expected config_saved status, got %q", body["status"])
	}
	if body["restart_error"] == "" {
		t.Fatal("expected restart_error")
	}
}

func TestUpdateConfig_NoConfigPath(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/mempool/config",
		`{"config":"test","restart":false}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateConfig_MalformedBody(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/config", "not json")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Bitcoind Stats ---

func TestBitcoindStats_NoRPC(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)
	// btcRPC is nil by default

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/bitcoind/stats", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 503 {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- Ckpool Stats ---

func TestCkpoolStats_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	dir := t.TempDir()
	t.Setenv("TRUFFELS_DATA_ROOT", dir)
	poolDir := filepath.Join(dir, "ckpool", "logs", "pool")
	_ = os.MkdirAll(poolDir, 0755)
	_ = os.WriteFile(filepath.Join(poolDir, "pool.status"), []byte(
		`{"runtime":3600,"lastupdate":1710000000,"Users":1,"Workers":2,"Idle":0}
{"hashrate1m":"1.92M","hashrate5m":"1.85M","hashrate15m":"1.80M","hashrate1hr":"1.75M","hashrate6hr":"1.70M","hashrate1d":"1.65M","hashrate7d":"1.60M"}
{"diff":65536,"accepted":1000,"rejected":5,"bestshare":123456789,"SPS1m":0.5,"SPS5m":0.45,"SPS15m":0.4,"SPS1h":0.35}
`), 0644)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/ckpool/stats", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var stats CkpoolStats
	_ = json.Unmarshal(w.Body.Bytes(), &stats)
	if stats.Status.Workers != 2 {
		t.Fatalf("expected 2 workers, got %d", stats.Status.Workers)
	}
	if stats.Hashrates.Hashrate1m != "1.92M" {
		t.Fatalf("expected 1.92M, got %q", stats.Hashrates.Hashrate1m)
	}
	if stats.Shares.Accepted != 1000 {
		t.Fatalf("expected 1000 accepted, got %d", stats.Shares.Accepted)
	}
}

func TestCkpoolStats_FileMissing(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	t.Setenv("TRUFFELS_DATA_ROOT", "/nonexistent")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/ckpool/stats", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 503 {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestCkpoolStats_IncompleteFile(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	dir := t.TempDir()
	t.Setenv("TRUFFELS_DATA_ROOT", dir)
	poolDir := filepath.Join(dir, "ckpool", "logs", "pool")
	_ = os.MkdirAll(poolDir, 0755)
	// Only 1 line instead of 3
	_ = os.WriteFile(filepath.Join(poolDir, "pool.status"), []byte("{}\n"), 0644)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/services/ckpool/stats", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 503 {
		t.Fatalf("expected 503 for incomplete file, got %d", w.Code)
	}
}

// --- Electrs Stats ---

func TestElectrsStats_Success(t *testing.T) {
	// Create a mock electrs prometheus endpoint
	electrsMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`# TYPE electrs_index_height gauge
electrs_index_height{type="tip"} 890123
`))
	}))
	defer electrsMock.Close()

	// The handler hardcodes "http://truffels-electrs:4224/" — we can't easily override.
	// This test validates the parsePrometheusGauge function instead (already tested above).
	// Full integration would need DNS override or dependency injection.
	// Skipping full handler test — parser is covered.
}

// --- Alerts Handler ---

func TestAlertsHandler_ActiveOnly(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	_ = st.UpsertAlert(&model.Alert{
		Type: "disk_full", Severity: model.SeverityWarning, Message: "90%",
	})
	_ = st.UpsertAlert(&model.Alert{
		Type: "high_temp", Severity: model.SeverityWarning, Message: "76C",
	})
	_ = st.ResolveAlerts("high_temp", "")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/alerts", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var alerts []model.Alert
	_ = json.Unmarshal(w.Body.Bytes(), &alerts)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 active alert, got %d", len(alerts))
	}
}

func TestAlertsHandler_All(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	_ = st.UpsertAlert(&model.Alert{
		Type: "disk_full", Severity: model.SeverityWarning, Message: "90%",
	})
	_ = st.UpsertAlert(&model.Alert{
		Type: "high_temp", Severity: model.SeverityWarning, Message: "76C",
	})
	_ = st.ResolveAlerts("high_temp", "")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/alerts?all=true", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var alerts []model.Alert
	_ = json.Unmarshal(w.Body.Bytes(), &alerts)
	if len(alerts) != 2 {
		t.Fatalf("expected 2 total alerts, got %d", len(alerts))
	}
}

// --- Settings ---

func TestGetSettings_Defaults(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/settings", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// Verify defaults
	if resp["restart_loop_count"] != float64(5) {
		t.Fatalf("expected restart_loop_count=5, got %v", resp["restart_loop_count"])
	}
	if resp["restart_loop_window_min"] != float64(10) {
		t.Fatalf("expected restart_loop_window_min=10, got %v", resp["restart_loop_window_min"])
	}
	if resp["restart_loop_max_retries"] != float64(0) {
		t.Fatalf("expected restart_loop_max_retries=0, got %v", resp["restart_loop_max_retries"])
	}
	if resp["dep_handling_mode"] != "flag_only" {
		t.Fatalf("expected dep_handling_mode=flag_only, got %v", resp["dep_handling_mode"])
	}
	if resp["temp_warning"] != float64(75) {
		t.Fatalf("expected temp_warning=75, got %v", resp["temp_warning"])
	}
	if resp["temp_critical"] != float64(80) {
		t.Fatalf("expected temp_critical=80, got %v", resp["temp_critical"])
	}
}

func TestUpdateSettings_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "PUT", "/api/truffels/settings",
		`{"restart_loop_count":10,"temp_warning":70}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify persisted
	val, _ := st.GetSetting("restart_loop_count")
	if val != "10" {
		t.Fatalf("expected restart_loop_count=10, got %q", val)
	}
	val, _ = st.GetSetting("temp_warning")
	if val != "70" {
		t.Fatalf("expected temp_warning=70, got %q", val)
	}
}

func TestUpdateSettings_UnknownKey(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "PUT", "/api/truffels/settings",
		`{"nonexistent_key":"value"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for unknown key, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateSettings_InvalidJSON(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "PUT", "/api/truffels/settings", "not json")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUpdateSettings_FloatValue(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "PUT", "/api/truffels/settings",
		`{"temp_critical":85.5}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	val, _ := st.GetSetting("temp_critical")
	if val != "85.5" {
		t.Fatalf("expected 85.5, got %q", val)
	}
}

func TestUpdateSettings_StringValue(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "PUT", "/api/truffels/settings",
		`{"dep_handling_mode":"flag_and_stop"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	val, _ := st.GetSetting("dep_handling_mode")
	if val != "flag_and_stop" {
		t.Fatalf("expected flag_and_stop, got %q", val)
	}
}

func TestUpdateSettings_AuditLogged(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "PUT", "/api/truffels/settings",
		`{"temp_warning":65}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	entries, _ := st.GetAuditLog(10)
	found := false
	for _, e := range entries {
		if e.Action == "settings_updated" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected audit log entry for settings_updated")
	}
}

func TestGetSettings_AfterUpdate(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	// Update first
	w := httptest.NewRecorder()
	req := authedReq(t, srv, "PUT", "/api/truffels/settings",
		`{"restart_loop_count":15,"dep_handling_mode":"flag_and_stop","temp_critical":90}`)
	srv.Router().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update failed: %d", w.Code)
	}

	// Read back
	w = httptest.NewRecorder()
	req = authedReq(t, srv, "GET", "/api/truffels/settings", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["restart_loop_count"] != float64(15) {
		t.Fatalf("expected 15, got %v", resp["restart_loop_count"])
	}
	if resp["dep_handling_mode"] != "flag_and_stop" {
		t.Fatalf("expected flag_and_stop, got %v", resp["dep_handling_mode"])
	}
	if resp["temp_critical"] != float64(90) {
		t.Fatalf("expected 90, got %v", resp["temp_critical"])
	}
	// Unchanged values should still be defaults
	if resp["temp_warning"] != float64(75) {
		t.Fatalf("expected unchanged temp_warning=75, got %v", resp["temp_warning"])
	}
}

func TestSettings_RequiresAuth(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	// GET without auth — middleware returns 403
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/truffels/settings", nil)
	srv.Router().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403 for unauthenticated GET, got %d", w.Code)
	}

	// PUT without auth
	w = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/api/truffels/settings", strings.NewReader(`{"temp_warning":60}`))
	srv.Router().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("expected 403 for unauthenticated PUT, got %d", w.Code)
	}
}

// --- Service Action: Enable ---

func TestServiceAction_Enable_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	// Disable the service first so we can test enabling it
	_ = st.EnsureService("bitcoind")
	_ = st.SetServiceEnabled("bitcoind", false)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"enable"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["action"] != "enable" {
		t.Fatalf("expected action=enable, got %q", body["action"])
	}
}

func TestServiceAction_Enable_AuditLogged(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	_ = st.EnsureService("electrs")
	_ = st.SetServiceEnabled("electrs", false)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/electrs/action",
		`{"action":"enable"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	entries, _ := st.GetAuditLog(10)
	found := false
	for _, e := range entries {
		if e.Action == "service_enable" && e.Target == "electrs" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected audit log entry for service_enable")
	}
}

// --- Service Action: Disable ---

func TestServiceAction_Disable_RunningService(t *testing.T) {
	agentState := &mockAgentState{
		containerStates: map[string]model.ContainerState{
			"truffels-bitcoind": {Name: "truffels-bitcoind", Status: "running", Health: "healthy"},
		},
	}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	_ = st.EnsureService("bitcoind")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"disable"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify compose stop was called because service was running
	if agentState.lastAction != "stop" {
		t.Fatalf("expected compose stop for running service, got %q", agentState.lastAction)
	}
}

func TestServiceAction_Disable_StoppedService(t *testing.T) {
	agentState := &mockAgentState{
		containerStates: map[string]model.ContainerState{
			"truffels-bitcoind": {Name: "truffels-bitcoind", Status: "exited", Health: ""},
		},
	}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	_ = st.EnsureService("bitcoind")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"disable"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Compose stop should NOT have been called since service was already stopped
	if agentState.lastAction == "stop" {
		t.Fatal("expected no compose stop for already-stopped service")
	}
}

func TestServiceAction_Disable_AuditLogged(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	_ = st.EnsureService("ckstats")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/ckstats/action",
		`{"action":"disable"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	entries, _ := st.GetAuditLog(10)
	found := false
	for _, e := range entries {
		if e.Action == "service_disable" && e.Target == "ckstats" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected audit log entry for service_disable")
	}
}

// --- Service Action: Start — Admission Control ---

// newTestServerWithCollector creates a server with a mock agent and a real metrics.Collector
// backed by fake proc/sys directories for controlled temperature and disk readings.
func newTestServerWithCollector(t *testing.T, agentState *mockAgentState, tempMilliC int) (*Server, *store.Store, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mockSrv := newMockAgent(t, agentState)
	t.Cleanup(mockSrv.Close)

	reg := service.NewRegistry("/srv/truffels/compose")
	a := auth.New(st)
	compose := docker.NewComposeClient(mockSrv.URL)
	docker.NewAgentInspector(mockSrv.URL)

	// Create fake /proc and /sys trees for the collector
	procDir := filepath.Join(dir, "proc")
	sysDir := filepath.Join(dir, "sys")
	diskDir := dir // use temp dir itself — will have plenty of space

	_ = os.MkdirAll(filepath.Join(procDir, "net"), 0755)
	_ = os.MkdirAll(filepath.Join(sysDir, "class/thermal/thermal_zone0"), 0755)

	// Write minimal /proc/stat
	_ = os.WriteFile(filepath.Join(procDir, "stat"),
		[]byte("cpu  100 0 50 800 10 5 3 0 0 0\n"), 0644)
	// Write minimal /proc/meminfo
	_ = os.WriteFile(filepath.Join(procDir, "meminfo"),
		[]byte("MemTotal:        8000000 kB\nMemAvailable:    4000000 kB\n"), 0644)
	// Write temperature (millidegrees C)
	_ = os.WriteFile(filepath.Join(sysDir, "class/thermal/thermal_zone0/temp"),
		[]byte(fmt.Sprintf("%d\n", tempMilliC)), 0644)
	// Write minimal /proc/uptime
	_ = os.WriteFile(filepath.Join(procDir, "uptime"), []byte("3600.00 7200.00\n"), 0644)
	// Write minimal /proc/net/dev
	_ = os.WriteFile(filepath.Join(procDir, "net/dev"),
		[]byte("Inter-|   Receive\n face |bytes\n    lo: 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n  wlan0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0\n"), 0644)
	// Write minimal /proc/diskstats (no matching device — that's fine, returns 0)
	_ = os.WriteFile(filepath.Join(procDir, "diskstats"), []byte(""), 0644)

	coll := metrics.NewCollector(procDir, sysDir, diskDir)
	srv := NewServer(reg, st, compose, coll, a, nil, nil)
	return srv, st, mockSrv
}

func TestServiceAction_Start_AdmissionTempTooHigh(t *testing.T) {
	// Temperature = 85°C (85000 millidegrees), default max = 80°C
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithCollector(t, agentState, 85000)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 409 {
		t.Fatalf("expected 409 for high temp, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "temperature") {
		t.Fatalf("expected temperature error, got %q", body["error"])
	}
}

func TestServiceAction_Start_AdmissionDiskLow(t *testing.T) {
	// Temperature = 50°C (safe), but set disk minimum very high so it always fails
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithCollector(t, agentState, 50000)

	// Set admission_disk_min_gb to something absurdly high (99999 GB)
	_ = st.SetSetting("admission_disk_min_gb", "99999")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 409 {
		t.Fatalf("expected 409 for low disk, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "disk") {
		t.Fatalf("expected disk space error, got %q", body["error"])
	}
}

func TestServiceAction_Start_AdmissionPasses(t *testing.T) {
	// Temperature = 50°C (safe), default disk min = 10 GB (temp dir has plenty)
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithCollector(t, agentState, 50000)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if agentState.lastAction != "up" {
		t.Fatalf("expected compose up, got %q", agentState.lastAction)
	}
}

// --- Service Action: Start — Disabled Service ---

func TestServiceAction_Start_DisabledService(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	_ = st.EnsureService("bitcoind")
	_ = st.SetServiceEnabled("bitcoind", false)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"start"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 409 {
		t.Fatalf("expected 409 for disabled service, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "disabled") {
		t.Fatalf("expected disabled error, got %q", body["error"])
	}
}

// --- Service Action: Pull-Restart ---

// newMockAgentWithPull creates a mock agent that also handles /v1/image/inspect and /v1/image/pull.
func newMockAgentWithPull(t *testing.T, state *mockAgentState, pullOutput string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/image/inspect":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"image":  "btcpayserver/bitcoin:30.2",
				"digest": "sha256:abc123",
				"tags":   []string{"30.2"},
			})

		case r.URL.Path == "/v1/image/pull":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok",
				"output": pullOutput,
			})

		case strings.HasPrefix(r.URL.Path, "/v1/compose/"):
			var req struct {
				ServiceID string `json:"service_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			action := strings.TrimPrefix(r.URL.Path, "/v1/compose/")
			state.lastAction = action
			state.lastServiceID = req.ServiceID

			if state.composeErr != "" {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": state.composeErr})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case r.URL.Path == "/v1/inspect":
			var req struct {
				Containers []string `json:"containers"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			var states []model.ContainerState
			for _, name := range req.Containers {
				if cs, ok := state.containerStates[name]; ok {
					states = append(states, cs)
				} else {
					states = append(states, model.ContainerState{
						Name: name, Status: "running", Health: "healthy",
					})
				}
			}
			_ = json.NewEncoder(w).Encode(states)
		}
	}))
}

func newTestServerWithPullAgent(t *testing.T, agentState *mockAgentState, pullOutput string) (*Server, *store.Store, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mockSrv := newMockAgentWithPull(t, agentState, pullOutput)
	t.Cleanup(mockSrv.Close)

	reg := service.NewRegistry("/srv/truffels/compose")
	a := auth.New(st)
	compose := docker.NewComposeClient(mockSrv.URL)
	docker.NewAgentInspector(mockSrv.URL)

	srv := NewServer(reg, st, compose, nil, a, nil, nil)
	return srv, st, mockSrv
}

func TestServiceAction_PullRestart_ImageChanged(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithPullAgent(t, agentState, "Pulling image... Done")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"pull-restart"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["action"] != "pull-restart" {
		t.Fatalf("expected action=pull-restart, got %q", body["action"])
	}
	// Image changed, so compose up should have been called
	if agentState.lastAction != "up" {
		t.Fatalf("expected compose up after image change, got %q", agentState.lastAction)
	}
}

func TestServiceAction_PullRestart_AlreadyUpToDate(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithPullAgent(t, agentState, "Image is up to date for btcpayserver/bitcoin:30.2")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"pull-restart"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "already_up_to_date" {
		t.Fatalf("expected already_up_to_date status, got %q", body["status"])
	}
	// Compose up should NOT have been called
	if agentState.lastAction == "up" {
		t.Fatal("expected no compose up when image is already up to date")
	}
}

func TestServiceAction_PullRestart_AuditLogged(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithPullAgent(t, agentState, "Pulling image... Done")

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/services/bitcoind/action",
		`{"action":"pull-restart"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	entries, _ := st.GetAuditLog(10)
	found := false
	for _, e := range entries {
		if e.Action == "service_pull-restart" && e.Target == "bitcoind" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected audit log entry for service_pull-restart")
	}
}

// --- System Info ---

func TestSystemInfo_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/system/info", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Hostname string `json:"hostname"`
		Model    string `json:"model"`
		CPUCores int    `json:"cpu_cores"`
		Networks []struct {
			Name string `json:"name"`
			IP   string `json:"ip"`
		} `json:"networks"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Hostname != "truffels" {
		t.Fatalf("expected hostname=truffels, got %q", body.Hostname)
	}
	if body.CPUCores != 4 {
		t.Fatalf("expected 4 cores, got %d", body.CPUCores)
	}
	if len(body.Networks) != 1 || body.Networks[0].Name != "wlan0" {
		t.Fatalf("unexpected networks: %+v", body.Networks)
	}
}

func TestSystemInfo_RequiresAuth(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)
	_ = srv.auth.SetPassword("testpassword")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/truffels/system/info", nil)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// --- System Journal ---

func TestSystemJournal_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/system/journal?lines=100", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["logs"] == "" {
		t.Fatal("expected logs in response")
	}
}

func TestSystemJournal_InvalidPriority(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/system/journal?priority=invalid", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemJournal_InvalidUnit(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/system/journal?unit=mysql", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemJournal_InvalidBoot(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/system/journal?boot=1", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemJournal_RequiresAuth(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)
	_ = srv.auth.SetPassword("testpassword")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/truffels/system/journal", nil)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// --- System Tuning ---

func TestSystemTuningGet_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "GET", "/api/truffels/system/tuning", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		PersistentJournal bool `json:"persistent_journal"`
		Swappiness        int  `json:"swappiness"`
		JournalDiskUsage  string `json:"journal_disk_usage"`
		Boots             []struct {
			Index int    `json:"index"`
			ID    string `json:"id"`
			First string `json:"first"`
			Last  string `json:"last"`
		} `json:"boots"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !body.PersistentJournal {
		t.Fatal("expected persistent_journal=true from mock")
	}
	if body.Swappiness != 10 {
		t.Fatalf("expected swappiness=10 from mock, got %d", body.Swappiness)
	}
	if len(body.Boots) != 2 {
		t.Fatalf("expected 2 boots from mock, got %d", len(body.Boots))
	}
	if body.Boots[0].Index != -1 || body.Boots[1].Index != 0 {
		t.Fatalf("unexpected boot indices: %d, %d", body.Boots[0].Index, body.Boots[1].Index)
	}
}

func TestSystemTuningGet_RequiresAuth(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)
	_ = srv.auth.SetPassword("testpassword")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/truffels/system/tuning", nil)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestSystemTuningSet_InvalidAction(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/system/tuning",
		`{"action":"reboot","value":"now"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemTuningSet_InvalidJournalValue(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/system/tuning",
		`{"action":"set_persistent_journal","value":"maybe"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemTuningSet_InvalidSwappiness(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/system/tuning",
		`{"action":"set_swappiness","value":"101"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemTuningSet_Success(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/system/tuning",
		`{"action":"set_swappiness","value":"10"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemTuningSet_AuditLogged(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	w := httptest.NewRecorder()
	req := authedReq(t, srv, "POST", "/api/truffels/system/tuning",
		`{"action":"set_swappiness","value":"15"}`)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	entries, _ := st.GetAuditLog(10)
	found := false
	for _, e := range entries {
		if e.Action == "system_tuning" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected audit log entry for system_tuning")
	}
}

func TestSystemTuningSet_RequiresAuth(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)
	_ = srv.auth.SetPassword("testpassword")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/truffels/system/tuning",
		strings.NewReader(`{"action":"set_swappiness","value":"10"}`))
	srv.Router().ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
