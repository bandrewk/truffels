package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"truffels-api/internal/auth"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

// newTestServer creates a Server with real store/registry/auth but nil compose/collector/btcRPC.
func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reg := service.NewRegistry("/srv/truffels/compose")
	a := auth.New(st)

	srv := NewServer(reg, st, nil, nil, a, nil, nil)
	return srv, st
}

// authenticatedRequest creates a request with a valid session cookie.
func authenticatedRequest(t *testing.T, srv *Server, method, path string, body string) *http.Request {
	t.Helper()
	// Set up auth first
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

// --- Health ---

func TestHandleHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/truffels/health", nil)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("expected ok, got %q", body["status"])
	}
	if body["version"] != "0.1.0" {
		t.Fatalf("expected 0.1.0, got %q", body["version"])
	}
}

// --- Auth ---

func TestAuthStatus_NotSetup(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/truffels/auth/status", nil)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]bool
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["setup"] {
		t.Fatal("expected setup=false")
	}
	if body["authenticated"] {
		t.Fatal("expected authenticated=false")
	}
}

func TestAuthSetup(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/truffels/auth/setup",
		strings.NewReader(`{"password":"testpassword"}`))
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Should have session cookie
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "truffels_session" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected session cookie after setup")
	}
}

func TestAuthSetup_TooShort(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/truffels/auth/setup",
		strings.NewReader(`{"password":"short"}`))
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for short password, got %d", w.Code)
	}
}

func TestAuthSetup_AlreadyConfigured(t *testing.T) {
	srv, _ := newTestServer(t)
	// Setup first time
	_ = srv.auth.SetPassword("testpassword")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/truffels/auth/setup",
		strings.NewReader(`{"password":"newpassword"}`))
	srv.Router().ServeHTTP(w, req)

	if w.Code != 409 {
		t.Fatalf("expected 409 for already configured, got %d", w.Code)
	}
}

func TestAuthLogin_Success(t *testing.T) {
	srv, _ := newTestServer(t)
	_ = srv.auth.SetPassword("testpassword")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/truffels/auth/login",
		strings.NewReader(`{"password":"testpassword"}`))
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthLogin_WrongPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	_ = srv.auth.SetPassword("testpassword")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/truffels/auth/login",
		strings.NewReader(`{"password":"wrongpassword"}`))
	srv.Router().ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthLogin_EmptyPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/truffels/auth/login",
		strings.NewReader(`{"password":""}`))
	srv.Router().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAuthLogin_RateLimit(t *testing.T) {
	srv, _ := newTestServer(t)
	_ = srv.auth.SetPassword("testpassword")

	// Reset rate limiter for this test
	loginLimiter.Lock()
	delete(loginLimiter.attempts, "192.0.2.1:1234")
	loginLimiter.Unlock()

	// Exhaust rate limit (5 attempts)
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/truffels/auth/login",
			strings.NewReader(`{"password":"wrong"}`))
		req.RemoteAddr = "192.0.2.1:1234"
		srv.Router().ServeHTTP(w, req)
	}

	// 6th should be rate limited
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/truffels/auth/login",
		strings.NewReader(`{"password":"wrong"}`))
	req.RemoteAddr = "192.0.2.1:1234"
	srv.Router().ServeHTTP(w, req)

	if w.Code != 429 {
		t.Fatalf("expected 429 rate limited, got %d", w.Code)
	}
}

func TestAuthLogout(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "POST", "/api/truffels/auth/logout", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Should clear cookie
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == "truffels_session" && c.MaxAge != -1 {
			t.Fatal("expected cookie cleared (MaxAge=-1)")
		}
	}
}

// --- Auth Middleware ---

func TestAuthMiddleware_NotSetup(t *testing.T) {
	srv, _ := newTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/truffels/services", nil)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403 setup_required, got %d", w.Code)
	}
}

func TestAuthMiddleware_Unauthorized(t *testing.T) {
	srv, _ := newTestServer(t)
	_ = srv.auth.SetPassword("testpassword")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/truffels/services", nil)
	srv.Router().ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// --- deriveState ---

func TestDeriveState_Empty(t *testing.T) {
	state := deriveState(nil)
	if state != model.StateUnknown {
		t.Fatalf("expected unknown, got %q", state)
	}
}

func TestDeriveState_AllRunningHealthy(t *testing.T) {
	containers := []model.ContainerState{
		{Name: "c1", Status: "running", Health: "healthy"},
		{Name: "c2", Status: "running", Health: "healthy"},
	}
	state := deriveState(containers)
	if state != model.StateRunning {
		t.Fatalf("expected running, got %q", state)
	}
}

func TestDeriveState_AllRunningNoHealth(t *testing.T) {
	containers := []model.ContainerState{
		{Name: "c1", Status: "running", Health: ""},
		{Name: "c2", Status: "running", Health: ""},
	}
	state := deriveState(containers)
	if state != model.StateRunning {
		t.Fatalf("expected running, got %q", state)
	}
}

func TestDeriveState_AllRunningOneUnhealthy(t *testing.T) {
	containers := []model.ContainerState{
		{Name: "c1", Status: "running", Health: "healthy"},
		{Name: "c2", Status: "running", Health: "unhealthy"},
	}
	state := deriveState(containers)
	if state != model.StateDegraded {
		t.Fatalf("expected degraded, got %q", state)
	}
}

func TestDeriveState_SomeRunning(t *testing.T) {
	containers := []model.ContainerState{
		{Name: "c1", Status: "running", Health: "healthy"},
		{Name: "c2", Status: "exited", Health: ""},
	}
	state := deriveState(containers)
	if state != model.StateDegraded {
		t.Fatalf("expected degraded, got %q", state)
	}
}

func TestDeriveState_AllStopped(t *testing.T) {
	containers := []model.ContainerState{
		{Name: "c1", Status: "exited", Health: ""},
		{Name: "c2", Status: "exited", Health: ""},
	}
	state := deriveState(containers)
	if state != model.StateStopped {
		t.Fatalf("expected stopped, got %q", state)
	}
}

func TestDeriveState_SingleRunning(t *testing.T) {
	containers := []model.ContainerState{
		{Name: "c1", Status: "running", Health: "healthy"},
	}
	state := deriveState(containers)
	if state != model.StateRunning {
		t.Fatalf("expected running, got %q", state)
	}
}

// --- simpleDiff ---

func TestSimpleDiff(t *testing.T) {
	tests := []struct {
		old, new, want string
	}{
		{"same", "same", "no changes"},
		{"", "new config", "initial config"},
		{"old", "new", "config updated"},
	}
	for _, tt := range tests {
		got := simpleDiff(tt.old, tt.new)
		if got != tt.want {
			t.Fatalf("simpleDiff(%q, %q) = %q, want %q", tt.old, tt.new, got, tt.want)
		}
	}
}

// --- parsePrometheusGauge ---

func TestParsePrometheusGauge(t *testing.T) {
	body := `# HELP electrs_index_height Indexed block height
# TYPE electrs_index_height gauge
electrs_index_height{type="tip"} 890123
electrs_index_height{type="db"} 890120
# HELP electrs_mempool_count
electrs_mempool_count 42
`
	got := parsePrometheusGauge(body, `electrs_index_height{type="tip"}`)
	if got != 890123 {
		t.Fatalf("expected 890123, got %d", got)
	}
}

func TestParsePrometheusGauge_NotFound(t *testing.T) {
	got := parsePrometheusGauge("nothing here\n", `electrs_index_height{type="tip"}`)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestParsePrometheusGauge_FloatValue(t *testing.T) {
	body := `electrs_index_height{type="tip"} 890123.0
`
	got := parsePrometheusGauge(body, `electrs_index_height{type="tip"}`)
	if got != 890123 {
		t.Fatalf("expected 890123, got %d", got)
	}
}

// --- splitLines ---

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"{}\n{}\n{}\n", 3},
		{"{}\n{}\n{}", 3},
		{"single", 1},
		{"\n\n\n", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := splitLines([]byte(tt.input))
		if len(got) != tt.want {
			t.Fatalf("splitLines(%q) = %d lines, want %d", tt.input, len(got), tt.want)
		}
	}
}

// --- AuditLog handler ---

func TestHandleAuditLog(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.LogAudit("test_action", "target", "detail", "127.0.0.1")

	w := httptest.NewRecorder()
	req := authenticatedRequest(t, srv, "GET", "/api/truffels/audit", "")
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var entries []map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &entries)
	// At least the test_action + setup audit from authenticatedRequest
	if len(entries) < 1 {
		t.Fatal("expected at least 1 audit entry")
	}
}

// --- System Restart / Shutdown ---

func TestSystemRestart_WrongPassword(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	req := authedReq(t, srv, "POST", "/api/truffels/system/restart",
		`{"password":"wrongpassword"}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemRestart_CorrectPassword(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	req := authedReq(t, srv, "POST", "/api/truffels/system/restart",
		`{"password":"testpassword"}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}
}

func TestSystemShutdown_CorrectPassword(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	req := authedReq(t, srv, "POST", "/api/truffels/system/shutdown",
		`{"password":"testpassword"}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}
}

func TestSystemRestart_NoPassword(t *testing.T) {
	agentState := &mockAgentState{}
	srv, _, _ := newTestServerWithAgent(t, agentState)

	req := authedReq(t, srv, "POST", "/api/truffels/system/restart", `{}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	// Empty password fails auth check → 401
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSystemRestart_AuditLog(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	req := authedReq(t, srv, "POST", "/api/truffels/system/restart",
		`{"password":"testpassword"}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	entries, err := st.GetAuditLog(50)
	if err != nil {
		t.Fatalf("get audit log: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Action == "system_restart" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected system_restart audit entry")
	}
}

func TestSystemShutdown_AuditLog(t *testing.T) {
	agentState := &mockAgentState{}
	srv, st, _ := newTestServerWithAgent(t, agentState)

	req := authedReq(t, srv, "POST", "/api/truffels/system/shutdown",
		`{"password":"testpassword"}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	entries, err := st.GetAuditLog(50)
	if err != nil {
		t.Fatalf("get audit log: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Action == "system_shutdown" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected system_shutdown audit entry")
	}
}

// --- Backup Download path traversal ---

func TestBackupDownload_PathTraversal(t *testing.T) {
	srv, _ := newTestServer(t)

	tests := []struct {
		filename string
		wantCode int
	}{
		{"../../etc/passwd", 400},
		{"../secrets/rpc.env", 400},
		{"valid.tar.gz", 404}, // valid name but doesn't exist
		{"", 400},
	}

	for _, tt := range tests {
		w := httptest.NewRecorder()
		req := authenticatedRequest(t, srv, "GET",
			"/api/truffels/backup/download?filename="+tt.filename, "")
		srv.Router().ServeHTTP(w, req)

		if w.Code != tt.wantCode {
			t.Fatalf("filename=%q: expected %d, got %d", tt.filename, tt.wantCode, w.Code)
		}
	}
}
