package docker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"truffels-api/internal/model"
)

func TestComposeClient_Up(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/compose/up" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		var req agentServiceReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ServiceID != "bitcoind" {
			t.Fatalf("expected bitcoind, got %q", req.ServiceID)
		}

		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	err := client.Up("bitcoind")
	if err != nil {
		t.Fatalf("up: %v", err)
	}
}

func TestComposeClient_Down(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/compose/down" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	if err := client.Down("bitcoind"); err != nil {
		t.Fatalf("down: %v", err)
	}
}

func TestComposeClient_Restart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/compose/restart" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	if err := client.Restart("bitcoind"); err != nil {
		t.Fatalf("restart: %v", err)
	}
}

func TestComposeClient_Logs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req agentLogsReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ServiceID != "electrs" {
			t.Fatalf("expected electrs, got %q", req.ServiceID)
		}
		if req.Tail != 100 {
			t.Fatalf("expected tail 100, got %d", req.Tail)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"logs":   "line1\nline2\n",
		})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	logs, err := client.Logs("electrs", 100, "")
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if logs != "line1\nline2\n" {
		t.Fatalf("unexpected logs: %q", logs)
	}
}

func TestComposeClient_AgentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "compose failed"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	err := client.Up("bitcoind")
	if err == nil {
		t.Fatal("expected error from agent")
	}
}

func TestComposeClient_AgentUnreachable(t *testing.T) {
	client := NewComposeClient("http://127.0.0.1:1")
	err := client.Up("bitcoind")
	if err == nil {
		t.Fatal("expected error for unreachable agent")
	}
}

// --- Inspector ---

func TestInspectContainers_NoAgent(t *testing.T) {
	// Save and restore global
	old := agentClient
	agentClient = nil
	defer func() { agentClient = old }()

	states := InspectContainers([]string{"c1", "c2"})
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d", len(states))
	}
	for _, s := range states {
		if s.Status != "unknown" {
			t.Fatalf("expected unknown status, got %q", s.Status)
		}
	}
}

func TestInspectContainer_NoAgent(t *testing.T) {
	old := agentClient
	agentClient = nil
	defer func() { agentClient = old }()

	state, err := InspectContainer("c1")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if state.Status != "unknown" {
		t.Fatalf("expected unknown, got %q", state.Status)
	}
}

func TestInspectContainers_WithAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req inspectRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		states := make([]model.ContainerState, len(req.Containers))
		for i, name := range req.Containers {
			states[i] = model.ContainerState{
				Name:   name,
				Status: "running",
				Health: "healthy",
			}
		}
		_ = json.NewEncoder(w).Encode(states)
	}))
	defer srv.Close()

	old := agentClient
	agentClient = &AgentInspector{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}
	defer func() { agentClient = old }()

	states := InspectContainers([]string{"c1", "c2"})
	if len(states) != 2 {
		t.Fatalf("expected 2, got %d", len(states))
	}
	if states[0].Status != "running" {
		t.Fatalf("expected running, got %q", states[0].Status)
	}
}

func TestInspectContainers_AgentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	old := agentClient
	agentClient = &AgentInspector{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}
	defer func() { agentClient = old }()

	states := InspectContainers([]string{"c1"})
	if len(states) != 1 {
		t.Fatalf("expected 1 fallback state, got %d", len(states))
	}
	if states[0].Status != "unknown" {
		t.Fatalf("expected unknown fallback, got %q", states[0].Status)
	}
}

// --- Stop ---

func TestComposeClient_Stop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/compose/stop" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var req agentServiceReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ServiceID != "electrs" {
			t.Fatalf("expected electrs, got %q", req.ServiceID)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	if err := client.Stop("electrs"); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestComposeClient_Stop_AgentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "stop failed"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	err := client.Stop("electrs")
	if err == nil {
		t.Fatal("expected error from agent")
	}
}

// --- SystemAction ---

func TestComposeClient_SystemAction_Restart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/system/restart" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	if err := client.SystemAction("restart"); err != nil {
		t.Fatalf("system restart: %v", err)
	}
}

func TestComposeClient_SystemAction_Shutdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/system/shutdown" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	if err := client.SystemAction("shutdown"); err != nil {
		t.Fatalf("system shutdown: %v", err)
	}
}

func TestComposeClient_SystemAction_AgentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "permission denied"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	err := client.SystemAction("restart")
	if err == nil {
		t.Fatal("expected error from agent")
	}
}

func TestComposeClient_SystemAction_AgentUnreachable(t *testing.T) {
	client := NewComposeClient("http://127.0.0.1:1")
	err := client.SystemAction("shutdown")
	if err == nil {
		t.Fatal("expected error for unreachable agent")
	}
}

// --- GitCheckout ---

func TestComposeClient_GitCheckout_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/git/checkout" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["repo_dir"] != "/repo" || body["tag"] != "v0.2.0" {
			t.Errorf("unexpected body: %v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	if err := client.GitCheckout("/repo", "v0.2.0"); err != nil {
		t.Fatalf("git checkout: %v", err)
	}
}

func TestComposeClient_GitCheckout_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "checkout failed"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	err := client.GitCheckout("/repo", "v0.2.0")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- BuildWithArgs ---

func TestComposeClient_BuildWithArgs_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["service_id"] != "truffels-agent" {
			t.Errorf("unexpected service_id: %v", body["service_id"])
		}
		args, ok := body["build_args"].(map[string]interface{})
		if !ok || args["VERSION"] != "v0.2.0" {
			t.Errorf("unexpected build_args: %v", body["build_args"])
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	err := client.BuildWithArgs("truffels-agent", map[string]string{"VERSION": "v0.2.0"})
	if err != nil {
		t.Fatalf("build with args: %v", err)
	}
}

func TestComposeClient_BuildWithArgs_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "build failed"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	err := client.BuildWithArgs("truffels-agent", map[string]string{"VERSION": "v0.2.0"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- ComposeUpDetached ---

func TestComposeClient_ComposeUpDetached_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/compose/up-detached" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	if err := client.ComposeUpDetached("truffels-agent"); err != nil {
		t.Fatalf("compose up detached: %v", err)
	}
}

func TestComposeClient_ComposeUpDetached_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "nsenter failed"})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	err := client.ComposeUpDetached("truffels-agent")
	if err == nil {
		t.Fatal("expected error")
	}
}
