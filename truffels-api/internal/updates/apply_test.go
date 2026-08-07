package updates

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	imageInspectFail bool   // if true, /v1/image/inspect returns 500
	tagFail          bool   // if true, /v1/image/tag returns 500 (no staged rollback image)
	composeDirs      map[string]string // service_id -> compose dir path for rewrite-tags
	imageLabels      map[string]string // labels returned by /v1/image/inspect (NeedsBuild verification)
	tags             *tagRecorder      // records /v1/image/tag calls when set
	// labelsByRef serves /v1/image/inspect-by-name in the self-update mock,
	// keyed by the exact image ref requested. A ref with no entry answers with
	// no labels at all — that is how the real agent reports an image it cannot
	// resolve, and how an image built without the VERSION arg reaching the
	// labelling stage looks from here.
	labelsByRef map[string]map[string]string
	detached    *callCounter // counts /v1/compose/up-detached calls when set
	// rewriteFailFrom makes /v1/compose/rewrite-tags fail from the Nth call on
	// (1-based, 0 = never). Step 2 of the self-update is call 1 and the restore
	// after a refusal is call 2, so this can break the restore alone.
	rewriteFailFrom int
}

// callCounter counts handler hits from the httptest server goroutine.
type callCounter struct {
	mu sync.Mutex
	n  int
}

func (c *callCounter) inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}

func (c *callCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// tagCall is one /v1/image/tag request. JSON tags match the agent's wire format
// so the recorder decodes exactly what the client sent.
type tagCall struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// tagRecorder collects retag calls. The engine prunes in a background goroutine
// after a successful update, so access is behind a mutex.
type tagRecorder struct {
	mu    sync.Mutex
	calls []tagCall
}

func (r *tagRecorder) record(c tagCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

func (r *tagRecorder) snapshot() []tagCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tagCall(nil), r.calls...)
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

		case "/v1/compose/rewrite-tags":
			var req struct {
				ServiceID string   `json:"service_id"`
				Images    []string `json:"images"`
				OldTag    string   `json:"old_tag"`
				NewTag    string   `json:"new_tag"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if dir, ok := opts.composeDirs[req.ServiceID]; ok {
				composePath := filepath.Join(dir, "docker-compose.yml")
				data, _ := os.ReadFile(composePath)
				content := string(data)
				for _, img := range req.Images {
					pattern := fmt.Sprintf(`(image:\s*)%s:%s(@sha256:[a-f0-9]+)?`, regexp.QuoteMeta(img), regexp.QuoteMeta(req.OldTag))
					re, _ := regexp.Compile(pattern)
					content = re.ReplaceAllString(content, fmt.Sprintf("${1}%s:%s", img, req.NewTag))
				}
				_ = os.WriteFile(composePath, []byte(content), 0644)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/image/inspect":
			if opts.imageInspectFail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "no such container"})
				return
			}
			info := docker.ImageInfo{
				Image:  "mariadb:lts",
				Digest: "sha256:olddigest123",
				Labels: opts.imageLabels,
			}
			_ = json.NewEncoder(w).Encode(info)

		case "/v1/image/inspect-by-name":
			var req struct {
				Image string `json:"image"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if opts.imageInspectFail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "no such image"})
				return
			}
			_ = json.NewEncoder(w).Encode(docker.ImageInfo{Image: req.Image, Labels: opts.imageLabels})

		case "/v1/image/tag":
			var req tagCall
			_ = json.NewDecoder(r.Body).Decode(&req)
			if opts.tags != nil {
				opts.tags.record(req)
			}
			if opts.tagFail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "no such image"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

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

		case "/v1/image/remove":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

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
	cd := t.TempDir()
	agent := newMockAgent(mockAgentOpts{composeDirs: map[string]string{"mempool": cd}})
	defer agent.Close()

	composeDir := cd
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
	cd := t.TempDir()
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
		case "/v1/compose/rewrite-tags":
			var req struct {
				ServiceID string   `json:"service_id"`
				Images    []string `json:"images"`
				OldTag    string   `json:"old_tag"`
				NewTag    string   `json:"new_tag"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			composePath := filepath.Join(cd, "docker-compose.yml")
			data, _ := os.ReadFile(composePath)
			content := string(data)
			for _, img := range req.Images {
				pattern := fmt.Sprintf(`(image:\s*)%s:%s(@sha256:[a-f0-9]+)?`, regexp.QuoteMeta(img), regexp.QuoteMeta(req.OldTag))
				re, _ := regexp.Compile(pattern)
				content = re.ReplaceAllString(content, fmt.Sprintf("${1}%s:%s", img, req.NewTag))
			}
			_ = os.WriteFile(composePath, []byte(content), 0644)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
	defer agent.Close()

	composeDir := cd
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
	// The rebuilt image must report the requested ref, otherwise the engine
	// refuses to call the update a success (see the NeedsBuild tests).
	agent := newMockAgent(mockAgentOpts{
		imageLabels: map[string]string{SourceRefLabel: "def789abc012"},
	})
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
	rewrites := &callCounter{}
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
			if opts.detached != nil {
				opts.detached.inc()
			}
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})

		case "/v1/compose/rewrite-tags":
			var req struct {
				ServiceID string   `json:"service_id"`
				Images    []string `json:"images"`
				OldTag    string   `json:"old_tag"`
				NewTag    string   `json:"new_tag"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			rewrites.inc()
			if opts.rewriteFailFrom > 0 && rewrites.count() >= opts.rewriteFailFrom {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "write compose file: read-only file system"})
				return
			}
			if dir, ok := opts.composeDirs[req.ServiceID]; ok {
				composePath := filepath.Join(dir, "docker-compose.yml")
				data, _ := os.ReadFile(composePath)
				content := string(data)
				for _, img := range req.Images {
					// handleComposeRewriteTags ignores old_tag and replaces
					// whatever tag is on the line. Mirror that: step 2 of the
					// self-update sends an empty old_tag, and a mock keyed on it
					// would mangle the file instead of moving the tag.
					pattern := fmt.Sprintf(`(image:\s*)%s:[^\s@]+(@sha256:[a-f0-9]+)?`, regexp.QuoteMeta(img))
					re, _ := regexp.Compile(pattern)
					content = re.ReplaceAllString(content, fmt.Sprintf("${1}%s:%s", img, req.NewTag))
				}
				// handleComposeRewriteTags moves the VERSION build args along
				// with the tags (main.go:2138). The truffels stack builds all
				// three services from source, so this line is what the next
				// build stamps into the image label — leaving it on a rejected
				// version is the same lie as leaving the tag there.
				versionRe := regexp.MustCompile(`(VERSION:\s+)\S+`)
				content = versionRe.ReplaceAllString(content, "${1}"+req.NewTag)
				_ = os.WriteFile(composePath, []byte(content), 0644)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/image/inspect":
			info := docker.ImageInfo{
				Image: "truffels/agent:v0.1.0",
			}
			_ = json.NewEncoder(w).Encode(info)

		case "/v1/image/inspect-by-name":
			var req struct {
				Image string `json:"image"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if opts.imageInspectFail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "inspect failed"})
				return
			}
			// Mirrors the agent: an unknown image is still a 200, just with
			// nothing in it.
			_ = json.NewEncoder(w).Encode(docker.ImageInfo{
				Image:  req.Image,
				Labels: opts.labelsByRef[req.Image],
			})

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

// selfUpdateLabels builds the inspect-by-name answer for a self-update that
// stamped every image with the version it was asked to build.
func selfUpdateLabels(version string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, img := range []string{"truffels/agent", "truffels/api", "truffels/web"} {
		out[img+":"+version] = map[string]string{VersionLabel: version}
	}
	return out
}

// selfUpdateTemplates is the truffels stack as applySelfUpdate sees it.
func selfUpdateTemplates(composeDir string) []model.ServiceTemplate {
	return []model.ServiceTemplate{
		{
			ID: "truffels", ComposeDir: composeDir,
			ContainerNames: []string{"truffels-agent", "truffels-api", "truffels-web"},
			UpdateSource: &model.UpdateSource{
				Type: model.SourceGitHubRelease, Repo: "owner/repo",
				Images: []string{"truffels/agent", "truffels/api", "truffels/web"}, NeedsBuild: true,
			},
		},
	}
}

// writeSelfUpdateCompose lays down a compose file at the pre-update version.
func writeSelfUpdateCompose(t *testing.T, dir, version string) string {
	t.Helper()
	composePath := filepath.Join(dir, "docker-compose.yml")
	// Shaped like the real /srv/truffels/compose/truffels/docker-compose.yml:
	// all three services carry a build: block with a VERSION arg alongside the
	// image tag. Both move together, and both have to move back — a file left
	// with the rejected version in build.args would hand it to the next build.
	body := fmt.Sprintf(`services:
  agent:
    image: truffels/agent:%[1]s
    build:
      context: /repo/truffels-agent
      args:
        VERSION: %[1]s
  api:
    image: truffels/api:%[1]s
    build:
      context: /repo/truffels-api
      args:
        VERSION: %[1]s
  web:
    image: truffels/web:%[1]s
    build:
      context: /repo/truffels-web
      args:
        VERSION: %[1]s
`, version)
	if err := os.WriteFile(composePath, []byte(body), 0644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	return composePath
}

// composeTagProblems reports every way the compose file fails to sit at version:
// each of the three image lines and each of the three VERSION build args must be
// exactly that version and nothing else.
//
// It anchors whole lines rather than counting substrings. A count of
// occurrences passes the one shape this exists to catch — an appended rather
// than replaced tag, "image: truffels/agent:v0.2.0v0.1.0", which contains
// ":v0.2.0" exactly once and satisfies any Contains check for it.
func composeTagProblems(content, version string) []string {
	var problems []string
	for _, img := range []string{"truffels/agent", "truffels/api", "truffels/web"} {
		re := regexp.MustCompile(`(?m)^\s*image:\s*` + regexp.QuoteMeta(img+":"+version) + `\s*$`)
		if n := len(re.FindAllString(content, -1)); n != 1 {
			problems = append(problems, fmt.Sprintf("expected exactly one line naming %s:%s, found %d", img, version, n))
		}
	}
	argRe := regexp.MustCompile(`(?m)^\s*VERSION:\s*` + regexp.QuoteMeta(version) + `\s*$`)
	if n := len(argRe.FindAllString(content, -1)); n != 3 {
		problems = append(problems, fmt.Sprintf("expected 3 VERSION build args at %s, found %d", version, n))
	}
	return problems
}

// assertComposeTags fails unless all three services, image tag and build arg
// alike, sit exactly at version.
func assertComposeTags(t *testing.T, composePath, version string) {
	t.Helper()
	data, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	content := string(data)
	if problems := composeTagProblems(content, version); len(problems) > 0 {
		t.Errorf("compose file is not at %s: %s\n%s", version, strings.Join(problems, "; "), content)
	}
}

// The assertion has to reject the exact mangling that motivated it, or it is
// decoration. An appended tag survives every substring check for the version it
// claims to be at.
func TestComposeTagProblems(t *testing.T) {
	clean := `services:
  agent:
    image: truffels/agent:v0.2.0
    build:
      args:
        VERSION: v0.2.0
  api:
    image: truffels/api:v0.2.0
    build:
      args:
        VERSION: v0.2.0
  web:
    image: truffels/web:v0.2.0
    build:
      args:
        VERSION: v0.2.0
`
	if problems := composeTagProblems(clean, "v0.2.0"); len(problems) > 0 {
		t.Errorf("a correct file must pass, got: %v", problems)
	}

	// The mock bug: the new tag written in front of the old one instead of over
	// it. strings.Contains(content, "truffels/agent:v0.2.0") is true here and
	// strings.Count(content, ":v0.2.0") is still 3.
	mangled := strings.Replace(clean, "truffels/agent:v0.2.0", "truffels/agent:v0.2.0v0.1.0", 1)
	if strings.Count(mangled, ":v0.2.0") != 3 || !strings.Contains(mangled, "truffels/agent:v0.2.0") {
		t.Fatal("this fixture no longer reproduces the shape a counting check misses")
	}
	if problems := composeTagProblems(mangled, "v0.2.0"); len(problems) == 0 {
		t.Error("an appended tag must be rejected")
	}

	// A tag that was never written back at all.
	if problems := composeTagProblems(clean, "v0.1.0"); len(problems) != 4 {
		t.Errorf("expected all three images and the build args to be reported, got: %v", problems)
	}

	// The build args left behind while the image tags moved.
	staleArgs := strings.ReplaceAll(clean, "VERSION: v0.2.0", "VERSION: v0.1.0")
	if problems := composeTagProblems(staleArgs, "v0.2.0"); len(problems) != 1 {
		t.Errorf("expected the stale build args to be reported on their own, got: %v", problems)
	}
}

func TestApplySelfUpdate_Success(t *testing.T) {
	cd := t.TempDir()
	restarts := &callCounter{}
	agent := newSelfUpdateMockAgent(mockAgentOpts{
		composeDirs: map[string]string{"truffels": cd},
		labelsByRef: selfUpdateLabels("v0.2.0"),
		detached:    restarts,
	})
	defer agent.Close()

	composePath := writeSelfUpdateCompose(t, cd, "v0.1.0")
	tmpls := selfUpdateTemplates(cd)

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

	// Verify compose file was rewritten for all three — and only rewritten,
	// with no remnant of the old tag left behind.
	assertComposeTags(t, composePath, "v0.2.0")

	// Verify single update log is "restarting" (will be reconciled on startup)
	logs, _ := st.GetUpdateLogs("truffels", 5)
	if len(logs) == 0 {
		t.Fatal("expected update log for truffels")
	}
	if logs[0].Status != model.UpdateRestarting {
		t.Errorf("expected status restarting, got %s", logs[0].Status)
	}

	if restarts.count() != 1 {
		t.Errorf("expected exactly one detached restart, got %d", restarts.count())
	}
}

// --- Self-update build verification (the images must hold the version we asked
// for before anything restarts into them) ---

// selfUpdateVerifyCase drives one run of the shared verification harness.
type selfUpdateVerifyCase struct {
	name   string
	labels map[string]map[string]string
	want   string // substring the failure must name
}

func TestApplySelfUpdate_RefusesMisbuiltImages(t *testing.T) {
	// The new tag exists but a layer-cache hit left the old version stamped on
	// every image behind it.
	stale := selfUpdateLabels("v0.2.0")
	for ref := range stale {
		stale[ref] = map[string]string{VersionLabel: "v0.1.0"}
	}
	// Only web is wrong: agent and api verify fine, so a check that stopped
	// after the first image would sail past this one.
	lastWrong := selfUpdateLabels("v0.2.0")
	lastWrong["truffels/web:v0.2.0"] = map[string]string{VersionLabel: "dev"}
	// The label is missing entirely — also what the agent reports for an image
	// that does not exist, since it answers 200 with empty fields.
	noLabel := selfUpdateLabels("v0.2.0")
	noLabel["truffels/agent:v0.2.0"] = map[string]string{}
	// Present but blank. Empty is not "matching".
	blank := selfUpdateLabels("v0.2.0")
	blank["truffels/agent:v0.2.0"] = map[string]string{VersionLabel: "   "}

	cases := []selfUpdateVerifyCase{
		{name: "every image still holds the old version", labels: stale, want: `"v0.1.0"`},
		{name: "only the last image is wrong", labels: lastWrong, want: `"dev"`},
		{name: "version label missing", labels: noLabel, want: "carries no " + VersionLabel},
		{name: "version label blank", labels: blank, want: "carries no " + VersionLabel},
		{name: "no image under the new tag at all", labels: nil, want: "carries no " + VersionLabel},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cd := t.TempDir()
			restarts := &callCounter{}
			agent := newSelfUpdateMockAgent(mockAgentOpts{
				composeDirs: map[string]string{"truffels": cd},
				labelsByRef: tc.labels,
				detached:    restarts,
			})
			defer agent.Close()

			composePath := writeSelfUpdateCompose(t, cd, "v0.1.0")
			eng, st := newTestEngine(t, agent, selfUpdateTemplates(cd))

			_ = st.UpsertUpdateCheck(&model.UpdateCheck{
				ServiceID:      "truffels",
				CurrentVersion: "v0.1.0",
				LatestVersion:  "v0.2.0",
				HasUpdate:      true,
			})

			err := eng.ApplyUpdate("truffels")
			if err == nil {
				t.Fatal("expected the update to fail on build verification")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected %q in error, got: %s", tc.want, err)
			}

			// The whole point: nothing may restart into an unverified image.
			if restarts.count() != 0 {
				t.Errorf("detached restart must not run, got %d call(s)", restarts.count())
			}

			// Step 2 moved the compose file onto v0.2.0 before building. A
			// refusal that leaves it there hands the rejected version to the
			// next `up` from anywhere — and compose would build the missing
			// image itself, without the VERSION arg.
			assertComposeTags(t, composePath, "v0.1.0")

			logs, _ := st.GetUpdateLogs("truffels", 5)
			if len(logs) == 0 {
				t.Fatal("expected an update log")
			}
			if logs[0].Status != model.UpdateFailed {
				t.Errorf("expected status failed, got %s", logs[0].Status)
			}

			alerts, _ := st.GetActiveAlerts()
			found := false
			for _, a := range alerts {
				if a.Type == "update_failed" && a.ServiceID == "truffels" {
					found = true
				}
			}
			if !found {
				t.Error("expected an update_failed alert for truffels")
			}
		})
	}
}

// The build-failure path predates verifySelfBuild and left the same debris:
// step 2 had already moved the compose file onto the version that then failed
// to build.
func TestApplySelfUpdate_BuildFailureRestoresComposeTag(t *testing.T) {
	cd := t.TempDir()
	restarts := &callCounter{}
	agent := newSelfUpdateMockAgent(mockAgentOpts{
		composeDirs: map[string]string{"truffels": cd},
		buildFail:   true,
		detached:    restarts,
	})
	defer agent.Close()

	composePath := writeSelfUpdateCompose(t, cd, "v0.1.0")
	eng, st := newTestEngine(t, agent, selfUpdateTemplates(cd))

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
	assertComposeTags(t, composePath, "v0.1.0")
	if restarts.count() != 0 {
		t.Errorf("detached restart must not run, got %d call(s)", restarts.count())
	}
}

// When the compose file cannot be put back, the failure has to say so — a file
// left naming a rejected version needs a human, and silence is how it stays
// unnoticed until the next restart builds an unlabelled image over it.
func TestApplySelfUpdate_RestoreFailureIsNamed(t *testing.T) {
	cd := t.TempDir()
	agent := newSelfUpdateMockAgent(mockAgentOpts{
		composeDirs: map[string]string{"truffels": cd},
		buildFail:   true,
		// Call 1 is step 2's rewrite onto v0.2.0; call 2 is the restore.
		rewriteFailFrom: 2,
	})
	defer agent.Close()

	writeSelfUpdateCompose(t, cd, "v0.1.0")
	eng, st := newTestEngine(t, agent, selfUpdateTemplates(cd))

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
	if !strings.Contains(err.Error(), "still names v0.2.0") {
		t.Errorf("expected the stranded compose file to be named in the error, got: %s", err)
	}
	if !strings.Contains(err.Error(), `restore the image tags to "v0.1.0"`) {
		t.Errorf("expected the error to say what to restore, got: %s", err)
	}
	logs, _ := st.GetUpdateLogs("truffels", 5)
	if len(logs) == 0 || !strings.Contains(logs[0].Error, "still names v0.2.0") {
		t.Errorf("expected the update log to carry the same warning, got: %+v", logs)
	}
}

// With no known previous version there is no tag to write back. Say that
// instead of inventing one or leaving the file quietly on the rejected version.
func TestApplySelfUpdate_UnknownPreviousVersionIsReported(t *testing.T) {
	cd := t.TempDir()
	agent := newSelfUpdateMockAgent(mockAgentOpts{
		composeDirs: map[string]string{"truffels": cd},
		buildFail:   true,
	})
	defer agent.Close()

	writeSelfUpdateCompose(t, cd, "v0.1.0")
	eng, st := newTestEngine(t, agent, selfUpdateTemplates(cd))

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "truffels",
		CurrentVersion: "",
		LatestVersion:  "v0.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("truffels")
	if err == nil {
		t.Fatal("expected error for build failure")
	}
	if !strings.Contains(err.Error(), "the previous version is unknown") {
		t.Errorf("expected the unknown previous version to be reported, got: %s", err)
	}
}

// A build that cannot be inspected at all is not a build that passed.
func TestApplySelfUpdate_InspectFailureBlocksRestart(t *testing.T) {
	cd := t.TempDir()
	restarts := &callCounter{}
	agent := newSelfUpdateMockAgent(mockAgentOpts{
		composeDirs:      map[string]string{"truffels": cd},
		imageInspectFail: true,
		detached:         restarts,
	})
	defer agent.Close()

	composePath := writeSelfUpdateCompose(t, cd, "v0.1.0")
	eng, st := newTestEngine(t, agent, selfUpdateTemplates(cd))

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "truffels",
		CurrentVersion: "v0.1.0",
		LatestVersion:  "v0.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("truffels")
	if err == nil {
		t.Fatal("expected the update to fail when the built image cannot be inspected")
	}
	if !strings.Contains(err.Error(), "cannot verify") {
		t.Errorf("expected 'cannot verify' in error, got: %s", err)
	}
	if restarts.count() != 0 {
		t.Errorf("detached restart must not run, got %d call(s)", restarts.count())
	}
	assertComposeTags(t, composePath, "v0.1.0")
	logs, _ := st.GetUpdateLogs("truffels", 5)
	if len(logs) == 0 || logs[0].Status != model.UpdateFailed {
		t.Error("expected a failed update log")
	}
}

// selfUpdateImage must pair the build loop with the declared image list, and
// say so rather than guess when the two do not line up.
func TestSelfUpdateImage(t *testing.T) {
	images := []string{"truffels/agent", "truffels/api", "truffels/web"}
	for _, svc := range []string{"agent", "api", "web"} {
		got, ok := selfUpdateImage(images, svc)
		if !ok || got != "truffels/"+svc {
			t.Errorf("svc %q: got %q ok=%v", svc, got, ok)
		}
	}
	if _, ok := selfUpdateImage(images, "nope"); ok {
		t.Error("an undeclared service must not resolve to an image")
	}
	if _, ok := selfUpdateImage(nil, "api"); ok {
		t.Error("an empty image list must not resolve to an image")
	}
	// The prefix is not what matches — the component name is.
	if got, ok := selfUpdateImage([]string{"ghcr.io/bandrewk/api"}, "api"); !ok || got != "ghcr.io/bandrewk/api" {
		t.Errorf("expected the declared ref back, got %q ok=%v", got, ok)
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
