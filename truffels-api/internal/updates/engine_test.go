package updates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"truffels-api/internal/model"
)

// helper writes content to a temp compose file and returns its path.
func writeTempCompose(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp compose: %v", err)
	}
	return path
}

// helper reads the file back.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func TestUpdateComposeImageTags_SimpleTagUpdate(t *testing.T) {
	compose := `version: "3.8"
services:
  backend:
    image: mempool/backend:v3.2.0
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mempool/backend:v3.2.1") {
		t.Errorf("expected image tag v3.2.1, got:\n%s", got)
	}
	if strings.Contains(got, "v3.2.0") {
		t.Errorf("old tag v3.2.0 should not remain, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_DigestPinned(t *testing.T) {
	compose := `version: "3.8"
services:
  backend:
    image: mempool/backend:v3.2.0@sha256:abc123def456789012345678901234567890123456789012345678901234abcd
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mempool/backend:v3.2.1") {
		t.Errorf("expected image tag v3.2.1, got:\n%s", got)
	}
	if strings.Contains(got, "@sha256:") {
		t.Errorf("digest pin should be stripped after update, got:\n%s", got)
	}
	if strings.Contains(got, "v3.2.0") {
		t.Errorf("old tag v3.2.0 should not remain, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_MultipleImages(t *testing.T) {
	compose := `version: "3.8"
services:
  backend:
    image: mempool/backend:v3.2.0
    restart: unless-stopped
  frontend:
    image: mempool/frontend:v3.2.0
    restart: unless-stopped
  db:
    image: mariadb:lts
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	err := updateComposeImageTags(path, []string{"mempool/backend", "mempool/frontend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mempool/backend:v3.2.1") {
		t.Errorf("backend should be updated to v3.2.1, got:\n%s", got)
	}
	if !strings.Contains(got, "image: mempool/frontend:v3.2.1") {
		t.Errorf("frontend should be updated to v3.2.1, got:\n%s", got)
	}
	if !strings.Contains(got, "image: mariadb:lts") {
		t.Errorf("mariadb should remain unchanged, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_UnrelatedImageUnchanged(t *testing.T) {
	compose := `version: "3.8"
services:
  db:
    image: mariadb:lts
    restart: unless-stopped
  backend:
    image: mempool/backend:v3.2.0
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	// Only update mempool/backend, mariadb must stay
	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mariadb:lts") {
		t.Errorf("mariadb:lts should not be touched, got:\n%s", got)
	}
	if !strings.Contains(got, "image: mempool/backend:v3.2.1") {
		t.Errorf("backend should be updated, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_Rollback(t *testing.T) {
	compose := `version: "3.8"
services:
  backend:
    image: mempool/backend:v3.2.1
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	// Simulate rollback: revert from v3.2.1 back to v3.2.0
	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.1", "v3.2.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mempool/backend:v3.2.0") {
		t.Errorf("expected rollback to v3.2.0, got:\n%s", got)
	}
	if strings.Contains(got, "v3.2.1") {
		t.Errorf("new tag v3.2.1 should not remain after rollback, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_FileNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "docker-compose.yml")

	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "read compose file") {
		t.Errorf("expected 'read compose file' in error, got: %v", err)
	}
}

func TestUpdateComposeImageTags_NoMatchingImages(t *testing.T) {
	compose := `version: "3.8"
services:
  db:
    image: mariadb:lts
    restart: unless-stopped
  proxy:
    image: caddy:2.9-alpine
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)
	original := readFile(t, path)

	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if got != original {
		t.Errorf("file should be unchanged when no images match.\nbefore:\n%s\nafter:\n%s", original, got)
	}
}

// --- RollbackService tests ---

func TestRollbackService_UnknownService(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, _ := newTestEngine(t, agent, nil)

	err := eng.RollbackService("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
	if !strings.Contains(err.Error(), "unknown service") {
		t.Errorf("expected 'unknown service' error, got: %v", err)
	}
}

func TestRollbackService_NoPreviousUpdate(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-electrs"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"getumbrel/electrs"},
		},
	}

	eng, _ := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	err := eng.RollbackService("electrs")
	if err == nil {
		t.Fatal("expected error for no previous version")
	}
	if !strings.Contains(err.Error(), "no previous version") {
		t.Errorf("expected 'no previous version' error, got: %v", err)
	}
}

func TestRollbackService_FloatingTag(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:             "mempool-db",
		DisplayName:    "mempool.space DB",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-mempool-db"},
		FloatingTag:    true,
		UpdateSource: &model.UpdateSource{
			Type:      model.SourceDockerDigest,
			Images:    []string{"mariadb"},
			TagFilter: "lts",
		},
	}

	eng, _ := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	err := eng.RollbackService("mempool-db")
	if err == nil {
		t.Fatal("expected error for floating-tag rollback")
	}
	if !strings.Contains(err.Error(), "floating-tag") {
		t.Errorf("expected 'floating-tag' in error, got: %v", err)
	}
}

func TestRollbackService_NoUpdateSource(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:          "proxy",
		DisplayName: "Caddy",
		ComposeDir:  t.TempDir(),
	}

	eng, _ := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	err := eng.RollbackService("proxy")
	if err == nil {
		t.Fatal("expected error for no update source")
	}
	if !strings.Contains(err.Error(), "no update source") {
		t.Errorf("expected 'no update source' error, got: %v", err)
	}
}

func TestRollbackService_AlreadyUpdating(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-electrs"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"getumbrel/electrs"},
		},
	}

	eng, _ := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	eng.mu.Lock()
	eng.updating["electrs"] = true
	eng.mu.Unlock()

	err := eng.RollbackService("electrs")
	if err == nil {
		t.Fatal("expected error for already updating")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected 'already in progress' error, got: %v", err)
	}
}

func TestRollbackService_Success(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	os.WriteFile(composePath, []byte(`services:
  backend:
    image: mempool/backend:v3.2.1
  frontend:
    image: mempool/frontend:v3.2.1
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

	// Seed a completed update log (so there is a previous version to roll back to)
	st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "mempool",
		FromVersion: "v3.2.0",
		ToVersion:   "v3.2.1",
		Status:      model.UpdateDone,
	})

	// Seed current update check showing current version
	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool",
		CurrentVersion: "v3.2.1",
		LatestVersion:  "v3.2.1",
		HasUpdate:      false,
	})

	err := eng.RollbackService("mempool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify compose file was rewritten back to v3.2.0
	data, _ := os.ReadFile(composePath)
	content := string(data)
	if !strings.Contains(content, "mempool/backend:v3.2.0") {
		t.Errorf("expected backend rolled back to v3.2.0, got:\n%s", content)
	}
	if !strings.Contains(content, "mempool/frontend:v3.2.0") {
		t.Errorf("expected frontend rolled back to v3.2.0, got:\n%s", content)
	}

	// Verify update log was created for rollback
	logs, _ := st.GetUpdateLogs("mempool", 10)
	if len(logs) < 2 {
		t.Fatalf("expected at least 2 update logs, got %d", len(logs))
	}
	// Most recent log should be the rollback
	rollbackLog := logs[0]
	if rollbackLog.Status != model.UpdateDone {
		t.Errorf("expected rollback log status done, got %s", rollbackLog.Status)
	}
	if rollbackLog.FromVersion != "v3.2.1" {
		t.Errorf("expected rollback from v3.2.1, got %s", rollbackLog.FromVersion)
	}
	if rollbackLog.ToVersion != "v3.2.0" {
		t.Errorf("expected rollback to v3.2.0, got %s", rollbackLog.ToVersion)
	}

	// Verify update check now shows update available (from rolled-back to current)
	check, _ := st.GetLatestUpdateCheck("mempool")
	if check == nil {
		t.Fatal("expected update check to exist after rollback")
	}
	if !check.HasUpdate {
		t.Error("expected HasUpdate to be true after rollback")
	}
	if check.CurrentVersion != "v3.2.0" {
		t.Errorf("expected current version v3.2.0, got %s", check.CurrentVersion)
	}
}

func TestRollbackService_AlreadyAtPreviousVersion(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-electrs"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"getumbrel/electrs"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	// Seed a completed update log
	st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "electrs",
		FromVersion: "v0.10.9",
		ToVersion:   "v0.11.0",
		Status:      model.UpdateDone,
	})

	// Current version is already at the previous version
	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "electrs",
		CurrentVersion: "v0.10.9",
		LatestVersion:  "v0.11.0",
		HasUpdate:      true,
	})

	err := eng.RollbackService("electrs")
	if err == nil {
		t.Fatal("expected error when already at previous version")
	}
	if !strings.Contains(err.Error(), "already at the previous version") {
		t.Errorf("expected 'already at the previous version' error, got: %v", err)
	}
}

func TestRollbackService_CustomBuildBlocked(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:             "ckpool",
		DisplayName:    "ckpool",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-ckpool"},
		UpdateSource: &model.UpdateSource{
			Type:       model.SourceBitbucket,
			Repo:       "ckolivas/ckpool",
			Branch:     "master",
			NeedsBuild: true,
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "ckpool",
		FromVersion: "abc123",
		ToVersion:   "def456",
		Status:      model.UpdateDone,
	})
	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "def456",
		LatestVersion:  "def456",
		HasUpdate:      false,
	})

	err := eng.RollbackService("ckpool")
	if err == nil {
		t.Fatal("expected error for custom-built service rollback")
	}
	if !strings.Contains(err.Error(), "custom-built") {
		t.Errorf("expected 'custom-built' in error, got: %v", err)
	}
}

func TestRollbackService_PullFails(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{pullFail: true})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	os.WriteFile(composePath, []byte(`services:
  server:
    image: getumbrel/electrs:v0.11.0
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-electrs"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"getumbrel/electrs"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "electrs",
		FromVersion: "v0.10.9",
		ToVersion:   "v0.11.0",
		Status:      model.UpdateDone,
	})
	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "electrs",
		CurrentVersion: "v0.11.0",
		LatestVersion:  "v0.11.0",
		HasUpdate:      false,
	})

	err := eng.RollbackService("electrs")
	if err == nil {
		t.Fatal("expected error from failed pull")
	}
	if !strings.Contains(err.Error(), "pull failed") {
		t.Errorf("expected 'pull failed' in error, got: %v", err)
	}
}

// --- RunPreflight tests ---

func TestRunPreflight_UnknownService(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, _ := newTestEngine(t, agent, nil)

	result, err := eng.RunPreflight("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CanProceed {
		t.Error("expected CanProceed to be false for unknown service")
	}
	if len(result.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(result.Checks))
	}
	if result.Checks[0].Name != "service_exists" {
		t.Errorf("expected check name 'service_exists', got %q", result.Checks[0].Name)
	}
	if result.Checks[0].Status != "fail" {
		t.Errorf("expected check status 'fail', got %q", result.Checks[0].Status)
	}
}

func TestRunPreflight_NoUpdateSource(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:          "proxy",
		DisplayName: "Caddy",
		ComposeDir:  t.TempDir(),
	}

	eng, _ := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	result, err := eng.RunPreflight("proxy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CanProceed {
		t.Error("expected CanProceed to be false for service without update source")
	}
	found := false
	for _, c := range result.Checks {
		if c.Name == "update_source" && c.Status == "fail" {
			found = true
		}
	}
	if !found {
		t.Error("expected a failing 'update_source' check")
	}
}

func TestRunPreflight_NoUpdateAvailable(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	os.WriteFile(composePath, []byte(`services:
  bitcoind:
    image: btcpayserver/bitcoin:30.2
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-bitcoind"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"btcpayserver/bitcoin"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	// No update available
	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "30.2",
		LatestVersion:  "30.2",
		HasUpdate:      false,
	})

	result, err := eng.RunPreflight("bitcoind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CanProceed {
		t.Error("expected CanProceed to be false when no update available")
	}
	found := false
	for _, c := range result.Checks {
		if c.Name == "update_available" && c.Status == "fail" {
			found = true
		}
	}
	if !found {
		t.Error("expected a failing 'update_available' check")
	}
}

func TestRunPreflight_AlreadyUpdating(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	os.WriteFile(composePath, []byte(`services:
  bitcoind:
    image: btcpayserver/bitcoin:30.0
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-bitcoind"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"btcpayserver/bitcoin"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "30.0",
		LatestVersion:  "30.2",
		HasUpdate:      true,
	})

	eng.mu.Lock()
	eng.updating["bitcoind"] = true
	eng.mu.Unlock()

	result, err := eng.RunPreflight("bitcoind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CanProceed {
		t.Error("expected CanProceed to be false when update in progress")
	}
	found := false
	for _, c := range result.Checks {
		if c.Name == "not_updating" && c.Status == "fail" {
			found = true
		}
	}
	if !found {
		t.Error("expected a failing 'not_updating' check")
	}
}

func TestRunPreflight_ComposeFileNotAccessible(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	// Use a non-existent compose dir
	composeDir := filepath.Join(t.TempDir(), "nonexistent")

	tmpl := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-bitcoind"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"btcpayserver/bitcoin"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "30.0",
		LatestVersion:  "30.2",
		HasUpdate:      true,
	})

	result, err := eng.RunPreflight("bitcoind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CanProceed {
		t.Error("expected CanProceed to be false when compose file not accessible")
	}
	found := false
	for _, c := range result.Checks {
		if c.Name == "compose_file" && c.Status == "fail" {
			found = true
		}
	}
	if !found {
		t.Error("expected a failing 'compose_file' check")
	}
}

func TestRunPreflight_DependencyUnhealthy(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{unhealthy: true})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	os.WriteFile(composePath, []byte(`services:
  electrs:
    image: getumbrel/electrs:v0.10.9
`), 0644)

	bitcoindTmpl := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-bitcoind"},
	}

	electrsTmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-electrs"},
		Dependencies:   []string{"bitcoind"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"getumbrel/electrs"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{bitcoindTmpl, electrsTmpl})

	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "electrs",
		CurrentVersion: "v0.10.9",
		LatestVersion:  "v0.11.0",
		HasUpdate:      true,
	})

	result, err := eng.RunPreflight("electrs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CanProceed {
		t.Error("expected CanProceed to be false when dependency is unhealthy")
	}
	found := false
	for _, c := range result.Checks {
		if c.Name == "dependency_bitcoind" && c.Status == "fail" {
			found = true
		}
	}
	if !found {
		t.Error("expected a failing 'dependency_bitcoind' check")
	}
}

func TestRunPreflight_DependentWarning(t *testing.T) {
	// Use healthy mock so dependencies pass and dependents are checked
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	os.WriteFile(composePath, []byte(`services:
  bitcoind:
    image: btcpayserver/bitcoin:30.0
`), 0644)

	bitcoindTmpl := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-bitcoind"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"btcpayserver/bitcoin"},
		},
	}

	electrsTmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-electrs"},
		Dependencies:   []string{"bitcoind"},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{bitcoindTmpl, electrsTmpl})

	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "30.0",
		LatestVersion:  "30.2",
		HasUpdate:      true,
	})

	result, err := eng.RunPreflight("bitcoind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Dependent warnings should NOT block
	found := false
	for _, c := range result.Checks {
		if c.Name == "dependent_electrs" {
			found = true
			if c.Status != "warn" {
				t.Errorf("expected dependent check status 'warn', got %q", c.Status)
			}
			if c.Blocking {
				t.Error("dependent check should not be blocking")
			}
		}
	}
	if !found {
		t.Error("expected a 'dependent_electrs' warning check")
	}
}

func TestRunPreflight_UpdateAvailable_SetsVersions(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	os.WriteFile(composePath, []byte(`services:
  bitcoind:
    image: btcpayserver/bitcoin:30.0
`), 0644)

	tmpl := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-bitcoind"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"btcpayserver/bitcoin"},
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "30.0",
		LatestVersion:  "30.2",
		HasUpdate:      true,
	})

	result, err := eng.RunPreflight("bitcoind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FromVersion != "30.0" {
		t.Errorf("expected FromVersion '30.0', got %q", result.FromVersion)
	}
	if result.ToVersion != "30.2" {
		t.Errorf("expected ToVersion '30.2', got %q", result.ToVersion)
	}
	found := false
	for _, c := range result.Checks {
		if c.Name == "update_available" && c.Status == "pass" {
			found = true
		}
	}
	if !found {
		t.Error("expected a passing 'update_available' check")
	}
}
