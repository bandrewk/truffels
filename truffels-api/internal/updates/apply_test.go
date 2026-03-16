package updates

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

// mockAgent creates an httptest server that simulates the truffels-agent.
// pullHandler is called for /v1/image/pull requests.
// upHandler is called for /v1/compose/up requests.
// downHandler is called for /v1/compose/down requests.
// inspectHandler is called for /v1/inspect requests.
type mockAgentOpts struct {
	pullFail    bool
	upFail      bool
	downFail    bool
	buildFail   bool
	unhealthy   bool // if true, inspect returns unhealthy containers
}

func newMockAgent(opts mockAgentOpts) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/image/pull":
			if opts.pullFail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "pull failed"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "output": "pulled"})

		case "/v1/compose/up":
			if opts.upFail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "start failed"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/compose/down":
			if opts.downFail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "stop failed"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/compose/build":
			if opts.buildFail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "build failed"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/image/inspect":
			info := docker.ImageInfo{
				Image:  "mariadb:lts",
				Digest: "sha256:olddigest123",
			}
			_ = json.NewEncoder(w).Encode(info)

		case "/v1/inspect":
			// AgentInspector.Inspect decodes as []model.ContainerState
			var req struct {
				Containers []string `json:"containers"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			var states []map[string]interface{}
			for _, name := range req.Containers {
				health := "healthy"
				if opts.unhealthy {
					health = "unhealthy"
				}
				states = append(states, map[string]interface{}{
					"name": name, "status": "running", "health": health,
				})
			}
			_ = json.NewEncoder(w).Encode(states)

		default:
			w.WriteHeader(404)
		}
	}))
}

// newTestEngine creates a test engine with a real store, custom registry, and mock agent.
func newTestEngine(t *testing.T, agent *httptest.Server, templates []model.ServiceTemplate) (*Engine, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	reg := service.NewTestRegistry(templates)
	compose := docker.NewComposeClient(agent.URL)
	// Set up agent inspector so docker.InspectContainer works with our mock
	docker.NewAgentInspector(agent.URL)
	eng := NewEngine(st, reg, compose)
	eng.healthWait = 0 // skip 30s wait in tests
	return eng, st
}

// --- ApplyUpdate: floating-tag (pull same tag, skip rewrite, no rollback) ---

func TestApplyUpdate_FloatingTag_Success(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  mempool-db:
    image: mariadb:lts
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "mempool-db",
		DisplayName:    "mempool.space DB",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-mempool-db"},
		FloatingTag:    true,
		UpdateSource: &model.UpdateSource{
			Type:      model.SourceDockerDigest,
			Images:    []string{"mariadb"},
			TagFilter: "lts",
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	// Seed an update check
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool-db",
		CurrentVersion: "sha256:olddigest123",
		LatestVersion:  "sha256:newdigest456",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("mempool-db")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify compose file was NOT rewritten (tag stays "lts")
	data, _ := os.ReadFile(composePath)
	if !strings.Contains(string(data), "mariadb:lts") {
		t.Errorf("compose file should still have mariadb:lts, got:\n%s", string(data))
	}

	// Verify update log was created and completed
	logs, _ := st.GetUpdateLogs("mempool-db", 10)
	if len(logs) == 0 {
		t.Fatal("expected at least one update log")
	}
	if logs[0].Status != model.UpdateDone {
		t.Errorf("expected status done, got %s", logs[0].Status)
	}
	if logs[0].FromVersion != "sha256:olddigest123" {
		t.Errorf("expected from sha256:olddigest123, got %s", logs[0].FromVersion)
	}

	// Verify update check was cleared
	check, _ := st.GetLatestUpdateCheck("mempool-db")
	if check == nil {
		t.Fatal("expected update check to exist")
	}
	if check.HasUpdate {
		t.Error("expected HasUpdate to be false after successful update")
	}
}

func TestApplyUpdate_FloatingTag_PullFails(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{pullFail: true})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  mempool-db:
    image: mariadb:lts
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "mempool-db",
		DisplayName:    "mempool.space DB",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-mempool-db"},
		FloatingTag:    true,
		UpdateSource: &model.UpdateSource{
			Type:      model.SourceDockerDigest,
			Images:    []string{"mariadb"},
			TagFilter: "lts",
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool-db",
		CurrentVersion: "sha256:olddigest123",
		LatestVersion:  "sha256:newdigest456",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("mempool-db")
	if err == nil {
		t.Fatal("expected error from failed pull")
	}
	if !strings.Contains(err.Error(), "pull failed") {
		t.Errorf("expected 'pull failed' in error, got: %v", err)
	}

	// Verify log shows failed status
	logs, _ := st.GetUpdateLogs("mempool-db", 10)
	if len(logs) == 0 {
		t.Fatal("expected at least one update log")
	}
	if logs[0].Status != model.UpdateFailed {
		t.Errorf("expected status failed, got %s", logs[0].Status)
	}
}

func TestApplyUpdate_FloatingTag_StartFails_NoRollback(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{upFail: true})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  mempool-db:
    image: mariadb:lts
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "mempool-db",
		DisplayName:    "mempool.space DB",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-mempool-db"},
		FloatingTag:    true,
		UpdateSource: &model.UpdateSource{
			Type:      model.SourceDockerDigest,
			Images:    []string{"mariadb"},
			TagFilter: "lts",
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool-db",
		CurrentVersion: "sha256:olddigest123",
		LatestVersion:  "sha256:newdigest456",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("mempool-db")
	if err == nil {
		t.Fatal("expected error from failed start")
	}
	if !strings.Contains(err.Error(), "start failed") {
		t.Errorf("expected 'start failed' in error, got: %v", err)
	}

	// Verify log shows failed (not rolled_back — floating tags can't rollback)
	logs, _ := st.GetUpdateLogs("mempool-db", 10)
	if len(logs) == 0 {
		t.Fatal("expected at least one update log")
	}
	if logs[0].Status != model.UpdateFailed {
		t.Errorf("expected status failed (no rollback for floating tag), got %s", logs[0].Status)
	}
	if !strings.Contains(logs[0].Error, "no rollback for floating tag") {
		t.Errorf("expected 'no rollback for floating tag' in error, got: %s", logs[0].Error)
	}
}

func TestApplyUpdate_FloatingTag_Unhealthy_NoRollback(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{unhealthy: true})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  mempool-db:
    image: mariadb:lts
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "mempool-db",
		DisplayName:    "mempool.space DB",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-mempool-db"},
		FloatingTag:    true,
		UpdateSource: &model.UpdateSource{
			Type:      model.SourceDockerDigest,
			Images:    []string{"mariadb"},
			TagFilter: "lts",
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool-db",
		CurrentVersion: "sha256:old",
		LatestVersion:  "sha256:new",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("mempool-db")
	if err == nil {
		t.Fatal("expected error for unhealthy after update")
	}

	// Should fail without rollback (floating tag)
	logs, _ := st.GetUpdateLogs("mempool-db", 10)
	if len(logs) == 0 {
		t.Fatal("expected at least one update log")
	}
	if logs[0].Status != model.UpdateFailed {
		t.Errorf("expected status failed, got %s", logs[0].Status)
	}
	if !strings.Contains(logs[0].Error, "no rollback for floating tag") {
		t.Errorf("expected 'no rollback for floating tag' in error, got: %s", logs[0].Error)
	}
}

func TestApplyUpdate_FloatingTag_SkipsComposeRewrite(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	original := `services:
  mempool-db:
    image: mariadb:lts
    restart: unless-stopped
`
	_ = os.WriteFile(composePath, []byte(original), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "mempool-db",
		DisplayName:    "mempool.space DB",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-mempool-db"},
		FloatingTag:    true,
		UpdateSource: &model.UpdateSource{
			Type:      model.SourceDockerDigest,
			Images:    []string{"mariadb"},
			TagFilter: "lts",
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool-db",
		CurrentVersion: "sha256:olddigest",
		LatestVersion:  "sha256:newdigest",
		HasUpdate:      true,
	})

	_ = eng.ApplyUpdate("mempool-db")

	// Compose file must be byte-identical to original
	data, _ := os.ReadFile(composePath)
	if string(data) != original {
		t.Errorf("compose file should be unchanged.\nbefore:\n%s\nafter:\n%s", original, string(data))
	}
}

// --- ApplyUpdate: standard tag-based (pull + rewrite + rollback) ---

func TestApplyUpdate_Standard_Success(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  backend:
    image: mempool/backend:v3.2.0
  frontend:
    image: mempool/frontend:v3.2.0
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "mempool",
		DisplayName:    "mempool.space",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-mempool-backend", "truffels-mempool-frontend"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"mempool/backend", "mempool/frontend"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool",
		CurrentVersion: "v3.2.0",
		LatestVersion:  "v3.2.1",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("mempool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify compose file was rewritten
	data, _ := os.ReadFile(composePath)
	content := string(data)
	if !strings.Contains(content, "mempool/backend:v3.2.1") {
		t.Errorf("expected backend to be updated to v3.2.1, got:\n%s", content)
	}
	if !strings.Contains(content, "mempool/frontend:v3.2.1") {
		t.Errorf("expected frontend to be updated to v3.2.1, got:\n%s", content)
	}
	if strings.Contains(content, "v3.2.0") {
		t.Errorf("old version v3.2.0 should not remain, got:\n%s", content)
	}
}

func TestApplyUpdate_Standard_StartFails_Rollback(t *testing.T) {
	// Agent that fails on first up call, then succeeds (rollback up)
	upCalls := 0
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/image/pull":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "output": "pulled"})
		case "/v1/compose/up":
			upCalls++
			if upCalls == 1 {
				// First up fails (update)
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "start failed"})
				return
			}
			// Second up succeeds (rollback)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/v1/compose/down":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  backend:
    image: mempool/backend:v3.2.0
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "mempool",
		DisplayName:    "mempool.space",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-mempool-backend"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"mempool/backend"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool",
		CurrentVersion: "v3.2.0",
		LatestVersion:  "v3.2.1",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("mempool")
	if err == nil {
		t.Fatal("expected error from failed start")
	}

	// Verify log shows rolled_back
	logs, _ := st.GetUpdateLogs("mempool", 10)
	if len(logs) == 0 {
		t.Fatal("expected at least one update log")
	}
	if logs[0].Status != model.UpdateRolledBack {
		t.Errorf("expected status rolled_back, got %s", logs[0].Status)
	}

	// Verify compose file was rolled back to old version
	data, _ := os.ReadFile(composePath)
	if !strings.Contains(string(data), "mempool/backend:v3.2.0") {
		t.Errorf("compose file should be rolled back to v3.2.0, got:\n%s", string(data))
	}
}

// --- ApplyUpdate: edge cases ---

func TestApplyUpdate_UnknownService(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, _ := newTestEngine(t, agent, nil)

	err := eng.ApplyUpdate("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
	if !strings.Contains(err.Error(), "unknown service") {
		t.Errorf("expected 'unknown service' error, got: %v", err)
	}
}

func TestApplyUpdate_NoUpdateSource(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:          "proxy",
		DisplayName: "Caddy",
		ComposeDir:  t.TempDir(),
	}

	eng, _ := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	err := eng.ApplyUpdate("proxy")
	if err == nil {
		t.Fatal("expected error for no update source")
	}
	if !strings.Contains(err.Error(), "no update source") {
		t.Errorf("expected 'no update source' error, got: %v", err)
	}
}

func TestApplyUpdate_NoUpdateAvailable(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:          "bitcoind",
		DisplayName: "Bitcoin Core",
		ComposeDir:  t.TempDir(),
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"btcpayserver/bitcoin"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	// No update check seeded, or check shows no update
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "30.2",
		LatestVersion:  "30.2",
		HasUpdate:      false,
	})

	err := eng.ApplyUpdate("bitcoind")
	if err == nil {
		t.Fatal("expected error for no update available")
	}
	if !strings.Contains(err.Error(), "no update available") {
		t.Errorf("expected 'no update available' error, got: %v", err)
	}
}

func TestApplyUpdate_AlreadyUpdating(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:          "bitcoind",
		DisplayName: "Bitcoin Core",
		ComposeDir:  t.TempDir(),
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"btcpayserver/bitcoin"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "30.0",
		LatestVersion:  "30.2",
		HasUpdate:      true,
	})

	// Simulate already updating
	eng.mu.Lock()
	eng.updating["bitcoind"] = true
	eng.mu.Unlock()

	err := eng.ApplyUpdate("bitcoind")
	if err == nil {
		t.Fatal("expected error for already updating")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected 'already in progress' error, got: %v", err)
	}
}

func TestApplyUpdate_BuildService(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	original := `services:
  ckpool:
    build: .
    image: truffels/ckpool:v1.0.0
`
	_ = os.WriteFile(composePath, []byte(original), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "ckpool",
		DisplayName:    "ckpool",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-ckpool"},
		UpdateSource: &model.UpdateSource{
			Type:       model.SourceBitbucket,
			Repo:       "ckolivas/ckpool",
			Branch:     "master",
			NeedsBuild: true,
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "abc123def456",
		LatestVersion:  "def789abc012",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("ckpool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Compose file should NOT be rewritten for build services
	data, _ := os.ReadFile(composePath)
	if string(data) != original {
		t.Errorf("compose file should be unchanged for build service.\nbefore:\n%s\nafter:\n%s", original, string(data))
	}

	// Verify log completed
	logs, _ := st.GetUpdateLogs("ckpool", 10)
	if len(logs) == 0 {
		t.Fatal("expected at least one update log")
	}
	if logs[0].Status != model.UpdateDone {
		t.Errorf("expected status done, got %s", logs[0].Status)
	}
}

func TestApplyUpdate_CreatesConfigSnapshot(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  db:
    image: mariadb:lts
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "mempool-db",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-mempool-db"},
		FloatingTag:    true,
		UpdateSource: &model.UpdateSource{
			Type:      model.SourceDockerDigest,
			Images:    []string{"mariadb"},
			TagFilter: "lts",
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool-db",
		CurrentVersion: "sha256:old",
		LatestVersion:  "sha256:new",
		HasUpdate:      true,
	})

	_ = eng.ApplyUpdate("mempool-db")

	// Verify a config revision snapshot was created
	revisions, _ := st.GetConfigRevisions("mempool-db", 10)
	if len(revisions) == 0 {
		t.Fatal("expected a config revision snapshot before update")
	}
	if revisions[0].Actor != "update_engine" {
		t.Errorf("expected actor 'update_engine', got %q", revisions[0].Actor)
	}
	if !strings.Contains(revisions[0].Diff, "pre-update snapshot") {
		t.Errorf("expected 'pre-update snapshot' in diff, got: %s", revisions[0].Diff)
	}
}

// --- Self-update (github_release) ---

func newSelfUpdateMockAgent(opts mockAgentOpts) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/git/checkout":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/compose/build":
			if opts.buildFail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "build failed"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/compose/up-detached":
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})

		case "/v1/image/inspect":
			info := docker.ImageInfo{
				Image: "truffels/agent:v0.1.0",
			}
			_ = json.NewEncoder(w).Encode(info)

		case "/v1/inspect":
			var req struct {
				Containers []string `json:"containers"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			var states []map[string]interface{}
			for _, name := range req.Containers {
				health := "healthy"
				if opts.unhealthy {
					health = "unhealthy"
				}
				states = append(states, map[string]interface{}{
					"name": name, "status": "running", "health": health,
				})
			}
			_ = json.NewEncoder(w).Encode(states)

		default:
			w.WriteHeader(404)
		}
	}))
}

func TestApplySelfUpdate_Success(t *testing.T) {
	agent := newSelfUpdateMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  agent:
    image: truffels/agent:v0.1.0
  api:
    image: truffels/api:v0.1.0
  web:
    image: truffels/web:v0.1.0
`), 0644)

	tmpls := []model.ServiceTemplate{
		{
			ID: "truffels", ComposeDir: composeDir,
			ContainerNames: []string{"truffels-agent", "truffels-api", "truffels-web"},
			UpdateSource: &model.UpdateSource{
				Type: model.SourceGitHubRelease, Repo: "owner/repo",
				Images: []string{"truffels/agent", "truffels/api", "truffels/web"}, NeedsBuild: true,
			},
		},
	}

	eng, st := newTestEngine(t, agent, tmpls)

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "truffels",
		CurrentVersion: "v0.1.0",
		LatestVersion:  "v0.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("truffels")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify compose file was rewritten for all three
	data, _ := os.ReadFile(composePath)
	content := string(data)
	if !strings.Contains(content, "truffels/agent:v0.2.0") {
		t.Error("expected agent image tag updated to v0.2.0")
	}
	if !strings.Contains(content, "truffels/api:v0.2.0") {
		t.Error("expected api image tag updated to v0.2.0")
	}
	if !strings.Contains(content, "truffels/web:v0.2.0") {
		t.Error("expected web image tag updated to v0.2.0")
	}

	// Verify single update log is "restarting" (will be reconciled on startup)
	logs, _ := st.GetUpdateLogs("truffels", 5)
	if len(logs) == 0 {
		t.Fatal("expected update log for truffels")
	}
	if logs[0].Status != model.UpdateRestarting {
		t.Errorf("expected status restarting, got %s", logs[0].Status)
	}
}

func TestApplySelfUpdate_BuildFailure(t *testing.T) {
	agent := newSelfUpdateMockAgent(mockAgentOpts{buildFail: true})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  agent:
    image: truffels/agent:v0.1.0
`), 0644)

	tmpls := []model.ServiceTemplate{
		{
			ID: "truffels", ComposeDir: composeDir,
			ContainerNames: []string{"truffels-agent", "truffels-api", "truffels-web"},
			UpdateSource: &model.UpdateSource{
				Type: model.SourceGitHubRelease, Repo: "owner/repo",
				Images: []string{"truffels/agent", "truffels/api", "truffels/web"}, NeedsBuild: true,
			},
		},
	}

	eng, st := newTestEngine(t, agent, tmpls)

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "truffels",
		CurrentVersion: "v0.1.0",
		LatestVersion:  "v0.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("truffels")
	if err == nil {
		t.Fatal("expected error for build failure")
	}
	if !strings.Contains(err.Error(), "build failed") {
		t.Errorf("expected 'build failed' in error, got: %s", err)
	}

	// Verify log shows failed
	logs, _ := st.GetUpdateLogs("truffels", 5)
	if len(logs) == 0 || logs[0].Status != model.UpdateFailed {
		t.Error("expected failed update log")
	}
}

// --- Startup reconciliation ---

func TestReconcileStuckUpdates_HealthyMarkedDone(t *testing.T) {
	agent := newSelfUpdateMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpls := []model.ServiceTemplate{
		{
			ID:             "truffels",
			ContainerNames: []string{"truffels-agent", "truffels-api", "truffels-web"},
		},
	}

	eng, st := newTestEngine(t, agent, tmpls)

	// Create a stuck "restarting" log
	logID, _ := st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "truffels",
		FromVersion: "v0.1.0",
		ToVersion:   "v0.2.0",
		Status:      model.UpdatePending,
	})
	_ = st.UpdateLogStatus(logID, model.UpdateRestarting, "", "v0.1.0")

	// Run reconciliation
	eng.reconcileStuckUpdates()

	// Verify it was marked done (mock agent returns healthy containers)
	logs, _ := st.GetUpdateLogs("truffels", 5)
	if len(logs) == 0 {
		t.Fatal("expected update log")
	}
	if logs[0].Status != model.UpdateDone {
		t.Errorf("expected done, got %s", logs[0].Status)
	}
}

func TestReconcileStuckUpdates_UnhealthyMarkedFailed(t *testing.T) {
	agent := newSelfUpdateMockAgent(mockAgentOpts{unhealthy: true})
	defer agent.Close()

	tmpls := []model.ServiceTemplate{
		{
			ID:             "truffels",
			ContainerNames: []string{"truffels-agent", "truffels-api", "truffels-web"},
		},
	}

	eng, st := newTestEngine(t, agent, tmpls)

	logID, _ := st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "truffels",
		FromVersion: "v0.1.0",
		ToVersion:   "v0.2.0",
		Status:      model.UpdatePending,
	})
	_ = st.UpdateLogStatus(logID, model.UpdateRestarting, "", "v0.1.0")

	eng.reconcileStuckUpdates()

	logs, _ := st.GetUpdateLogs("truffels", 5)
	if len(logs) == 0 {
		t.Fatal("expected update log")
	}
	if logs[0].Status != model.UpdateFailed {
		t.Errorf("expected failed, got %s", logs[0].Status)
	}
}

func TestReconcileStuckUpdates_NoStuckLogs(t *testing.T) {
	agent := newSelfUpdateMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpls := []model.ServiceTemplate{
		{ID: "truffels", ContainerNames: []string{"truffels-agent", "truffels-api", "truffels-web"}},
	}

	eng, _ := newTestEngine(t, agent, tmpls)

	// Should not panic with no stuck logs
	eng.reconcileStuckUpdates()
}
