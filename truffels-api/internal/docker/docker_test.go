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
		json.NewDecoder(r.Body).Decode(&req)
		if req.ServiceID != "bitcoind" {
			t.Fatalf("expected bitcoind, got %q", req.ServiceID)
		}

		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
		json.NewDecoder(r.Body).Decode(&req)
		if req.ServiceID != "electrs" {
			t.Fatalf("expected electrs, got %q", req.ServiceID)
		}
		if req.Tail != 100 {
			t.Fatalf("expected tail 100, got %d", req.Tail)
		}
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"logs":   "line1\nline2\n",
		})
	}))
	defer srv.Close()

	client := NewComposeClient(srv.URL)
	logs, err := client.Logs("electrs", 100)
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
		json.NewEncoder(w).Encode(map[string]string{"error": "compose failed"})
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
		json.NewDecoder(r.Body).Decode(&req)

		states := make([]model.ContainerState, len(req.Containers))
		for i, name := range req.Containers {
			states[i] = model.ContainerState{
				Name:   name,
				Status: "running",
				Health: "healthy",
			}
		}
		json.NewEncoder(w).Encode(states)
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
		w.Write([]byte("internal error"))
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
