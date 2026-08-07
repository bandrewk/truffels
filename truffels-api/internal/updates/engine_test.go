package updates

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
)

// --- getCheckInterval / isCheckEnabled tests ---

func TestGetCheckInterval_Default(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, _ := newTestEngine(t, agent, nil)

	got := eng.getCheckInterval()
	if got != 24*time.Hour {
		t.Fatalf("expected 24h default, got %v", got)
	}
}

func TestGetCheckInterval_CustomValue(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, st := newTestEngine(t, agent, nil)
	_ = st.SetSetting("update_check_interval_hours", "12")

	got := eng.getCheckInterval()
	if got != 12*time.Hour {
		t.Fatalf("expected 12h, got %v", got)
	}
}

func TestGetCheckInterval_BoundsLow(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, st := newTestEngine(t, agent, nil)
	_ = st.SetSetting("update_check_interval_hours", "0")

	got := eng.getCheckInterval()
	if got != 24*time.Hour {
		t.Fatalf("expected 24h fallback for 0, got %v", got)
	}
}

func TestGetCheckInterval_BoundsHigh(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, st := newTestEngine(t, agent, nil)
	_ = st.SetSetting("update_check_interval_hours", "200")

	got := eng.getCheckInterval()
	if got != 24*time.Hour {
		t.Fatalf("expected 24h fallback for 200, got %v", got)
	}
}

func TestGetCheckInterval_InvalidValue(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, st := newTestEngine(t, agent, nil)
	_ = st.SetSetting("update_check_interval_hours", "abc")

	got := eng.getCheckInterval()
	if got != 24*time.Hour {
		t.Fatalf("expected 24h fallback for invalid, got %v", got)
	}
}

func TestIsCheckEnabled_Default(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, _ := newTestEngine(t, agent, nil)

	if !eng.isCheckEnabled() {
		t.Fatal("expected enabled by default")
	}
}

func TestIsCheckEnabled_True(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, st := newTestEngine(t, agent, nil)
	_ = st.SetSetting("update_check_enabled", "true")

	if !eng.isCheckEnabled() {
		t.Fatal("expected enabled when set to true")
	}
}

func TestIsCheckEnabled_False(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	eng, st := newTestEngine(t, agent, nil)
	_ = st.SetSetting("update_check_enabled", "false")

	if eng.isCheckEnabled() {
		t.Fatal("expected disabled when set to false")
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
	cd := t.TempDir()
	agent := newMockAgent(mockAgentOpts{composeDirs: map[string]string{"mempool": cd}})
	defer agent.Close()

	composeDir := cd
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
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
	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "mempool",
		FromVersion: "v3.2.0",
		ToVersion:   "v3.2.1",
		Status:      model.UpdateDone,
	})

	// Seed current update check showing current version
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
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
	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "electrs",
		FromVersion: "v0.10.9",
		ToVersion:   "v0.11.0",
		Status:      model.UpdateDone,
	})

	// Current version is already at the previous version
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
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

// Custom-built services used to be refused outright here ("rollback not
// supported for custom-built services"). They now roll back by moving the
// staged :rollback tag back onto the ref the compose file runs — there is no
// registry to pull an old build from.
func TestRollbackService_CustomBuildRetagsPreviousImage(t *testing.T) {
	tags := &tagRecorder{}
	agent := newMockAgent(mockAgentOpts{tags: tags})
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

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "ckpool",
		FromVersion: "abc123",
		ToVersion:   "def456",
		Status:      model.UpdateDone,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "def456",
		LatestVersion:  "def456",
		HasUpdate:      false,
	})

	if err := eng.RollbackService("ckpool"); err != nil {
		t.Fatalf("rollback of a custom-built service should now succeed: %v", err)
	}

	calls := tags.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one retag, got %d: %+v", len(calls), calls)
	}
	if calls[0].Source != "truffels/ckpool:rollback" {
		t.Errorf("tag source = %q, want the staged rollback image", calls[0].Source)
	}
	if calls[0].Target != "truffels/ckpool:latest" {
		t.Errorf("tag target = %q, want the live tag", calls[0].Target)
	}

	// The version bookkeeping must follow, same as for registry services.
	check, _ := st.GetLatestUpdateCheck("ckpool")
	if check == nil || check.CurrentVersion != "abc123" {
		t.Errorf("expected current version abc123 after rollback, got %+v", check)
	}
	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if len(logs) == 0 || logs[0].Status != model.UpdateDone {
		t.Errorf("expected a done rollback log, got %+v", logs)
	}
}

// Without a staged image there is nothing to roll back to. Reporting success
// here is exactly the old bug: Down/Up would restart the broken build.
func TestRollbackService_CustomBuildFailsWithoutStagedImage(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{tagFail: true})
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

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "ckpool",
		FromVersion: "abc123",
		ToVersion:   "def456",
		Status:      model.UpdateDone,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "def456",
		LatestVersion:  "def456",
		HasUpdate:      false,
	})

	err := eng.RollbackService("ckpool")
	if err == nil {
		t.Fatal("expected an error when no rollback image is staged")
	}
	if !strings.Contains(err.Error(), "no rollback image available") {
		t.Errorf("expected 'no rollback image available', got: %v", err)
	}

	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if len(logs) == 0 || logs[0].Status != model.UpdateFailed {
		t.Errorf("expected a failed rollback log, got %+v", logs)
	}
}

func TestRollbackService_PullFails(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{pullFail: true})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
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

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "electrs",
		FromVersion: "v0.10.9",
		ToVersion:   "v0.11.0",
		Status:      model.UpdateDone,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
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
	_ = os.WriteFile(composePath, []byte(`services:
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
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
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
	_ = os.WriteFile(composePath, []byte(`services:
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

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
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

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
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
	_ = os.WriteFile(composePath, []byte(`services:
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

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
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
	_ = os.WriteFile(composePath, []byte(`services:
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

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
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

// --- checkService skip logic for non-existent containers ---

func TestCheckService_SkipsWhenContainerMissing(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{imageInspectFail: true})
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

	// No prior check exists — container doesn't exist → should skip
	eng.checkService(tmpl)

	check, err := st.GetLatestUpdateCheck("electrs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if check != nil {
		t.Errorf("expected no update check row for missing container, got current=%q latest=%q",
			check.CurrentVersion, check.LatestVersion)
	}
}

func TestCheckService_CleansStaleCheckWhenContainerMissing(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{imageInspectFail: true})
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

	// Seed a stale check with blank current version (the bug scenario)
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "electrs",
		CurrentVersion: "",
		LatestVersion:  "v0.11.0",
		HasUpdate:      false,
	})

	eng.checkService(tmpl)

	check, _ := st.GetLatestUpdateCheck("electrs")
	if check != nil {
		t.Errorf("expected stale check to be cleaned up, but it still exists")
	}
}

func TestCheckService_DoesNotSkipGitHubSource(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{imageInspectFail: true})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:             "ckpool",
		DisplayName:    "ckpool",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-ckpool"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceBitbucket,
			Repo:   "ckolivas/ckpool",
			Branch: "master",
		},
	}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	eng.checkService(tmpl)

	// Bitbucket source should NOT be skipped — it initializes current=latest
	check, _ := st.GetLatestUpdateCheck("ckpool")
	// The check should exist (even if both versions are empty due to mock,
	// the point is it wasn't skipped by the guard)
	if check == nil {
		t.Error("expected check to exist for Bitbucket source even with no container")
	}
}

func TestRunPreflight_UpdateAvailable_SetsVersions(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	composePath := filepath.Join(composeDir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
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

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
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

// --- pruneOldImages tests ---

func TestPruneOldImages_KeepsCurrentAndN1(t *testing.T) {
	var removedImages []string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/image/remove":
			var req struct{ Image string `json:"image"` }
			_ = json.NewDecoder(r.Body).Decode(&req)
			removedImages = append(removedImages, req.Image)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/v1/inspect":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"name": "truffels-electrs", "status": "running", "health": "healthy"},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
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

	// Create update history: v0.9 → v0.10 → v0.11 (current)
	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "electrs", FromVersion: "v0.9.0", ToVersion: "v0.10.0", Status: model.UpdateDone,
	})
	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "electrs", FromVersion: "v0.10.0", ToVersion: "v0.11.0", Status: model.UpdateDone,
	})

	eng.pruneOldImages("electrs", tmpl.UpdateSource)

	// Should keep v0.11.0 (current=toVersion) and v0.10.0 (N-1=fromVersion of most recent done)
	// Should remove v0.9.0
	if len(removedImages) != 1 {
		t.Fatalf("expected 1 image removed, got %d: %v", len(removedImages), removedImages)
	}
	if removedImages[0] != "getumbrel/electrs:v0.9.0" {
		t.Errorf("expected getumbrel/electrs:v0.9.0 removed, got %q", removedImages[0])
	}
}

func TestPruneOldImages_RespectsSetting(t *testing.T) {
	var removedImages []string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/image/remove":
			var req struct{ Image string `json:"image"` }
			_ = json.NewDecoder(r.Body).Decode(&req)
			removedImages = append(removedImages, req.Image)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/v1/inspect":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"name": "truffels-electrs", "status": "running", "health": "healthy"},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
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

	// Set keep_old_images to true
	_ = st.SetSetting("update_keep_old_images", "true")

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "electrs", FromVersion: "v0.9.0", ToVersion: "v0.10.0", Status: model.UpdateDone,
	})
	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "electrs", FromVersion: "v0.10.0", ToVersion: "v0.11.0", Status: model.UpdateDone,
	})

	eng.pruneOldImages("electrs", tmpl.UpdateSource)

	// Should NOT remove anything when setting is true
	if len(removedImages) != 0 {
		t.Fatalf("expected 0 images removed when keep_old_images=true, got %d: %v", len(removedImages), removedImages)
	}
}

// --- NeedsBuild apply path (ckpool / ckstats) ---

// needsBuildRecorder captures what the engine asked the agent to do on the
// NeedsBuild path and serves the image label the engine verifies against.
// The engine prunes in a background goroutine after a successful update, so
// every field is behind a mutex.
type needsBuildRecorder struct {
	mu             sync.Mutex
	imageLabels    map[string]string
	inspectFail    bool              // /v1/image/inspect 500s — container gone
	byNameLabels   map[string]string // labels served by /v1/image/inspect-by-name
	byNameImage    string            // ref the engine asked for by name
	byNameCalls    int
	tagFail        bool // /v1/image/tag 500s — nothing staged to restore
	tags           []tagCall
	buildArgs      map[string]string
	checkoutDir    string
	checkoutRef    string
	checkoutScheme string
	checkoutCalls  int
	upCalls        int
}

func (r *needsBuildRecorder) snapshot() needsBuildRecorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	return needsBuildRecorder{
		byNameImage:    r.byNameImage,
		byNameCalls:    r.byNameCalls,
		tags:           append([]tagCall(nil), r.tags...),
		buildArgs:      r.buildArgs,
		checkoutDir:    r.checkoutDir,
		checkoutRef:    r.checkoutRef,
		checkoutScheme: r.checkoutScheme,
		checkoutCalls:  r.checkoutCalls,
		upCalls:        r.upCalls,
	}
}

func newNeedsBuildAgent(rec *needsBuildRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/git/checkout":
			var req struct {
				RepoDir   string `json:"repo_dir"`
				Tag       string `json:"tag"`
				RefScheme string `json:"ref_scheme"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			rec.mu.Lock()
			rec.checkoutDir, rec.checkoutRef, rec.checkoutScheme = req.RepoDir, req.Tag, req.RefScheme
			rec.checkoutCalls++
			rec.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/compose/build":
			var req struct {
				ServiceID string            `json:"service_id"`
				BuildArgs map[string]string `json:"build_args"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			rec.mu.Lock()
			rec.buildArgs = req.BuildArgs
			rec.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/image/inspect":
			rec.mu.Lock()
			labels, fail := rec.imageLabels, rec.inspectFail
			rec.mu.Unlock()
			if fail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "cannot inspect container: no such container"})
				return
			}
			_ = json.NewEncoder(w).Encode(docker.ImageInfo{Image: "truffels/built:local", Labels: labels})

		case "/v1/image/inspect-by-name":
			var req struct {
				Image string `json:"image"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			rec.mu.Lock()
			rec.byNameImage = req.Image
			rec.byNameCalls++
			labels := rec.byNameLabels
			rec.mu.Unlock()
			if labels == nil {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "no such image"})
				return
			}
			_ = json.NewEncoder(w).Encode(docker.ImageInfo{Image: req.Image, Labels: labels})

		case "/v1/image/tag":
			var req tagCall
			_ = json.NewDecoder(r.Body).Decode(&req)
			rec.mu.Lock()
			rec.tags = append(rec.tags, req)
			fail := rec.tagFail
			rec.mu.Unlock()
			if fail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "no such image"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/compose/up":
			rec.mu.Lock()
			rec.upCalls++
			rec.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/inspect":
			var req struct {
				Containers []string `json:"containers"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			var states []map[string]interface{}
			for _, name := range req.Containers {
				states = append(states, map[string]interface{}{
					"name": name, "status": "running", "health": "healthy",
				})
			}
			_ = json.NewEncoder(w).Encode(states)

		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
}

func ckpoolBuildTemplate(composeDir string) model.ServiceTemplate {
	return model.ServiceTemplate{
		ID:             "ckpool",
		DisplayName:    "ckpool",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-ckpool"},
		UpdateSource: &model.UpdateSource{
			Type:       model.SourceBitbucket,
			Repo:       "ckolivas/ckpool",
			Branch:     "master",
			NeedsBuild: true,
			RefScheme:  model.RefSchemeTag,
			TagFilter:  "v",
		},
	}
}

func ckstatsBuildTemplate(composeDir, refScheme string) model.ServiceTemplate {
	return model.ServiceTemplate{
		ID:             "ckstats",
		DisplayName:    "ckstats",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-ckstats", "truffels-ckstats-cron"},
		UpdateSource: &model.UpdateSource{
			Type:       model.SourceGitHub,
			Repo:       "mrv777/ckstats",
			Branch:     "main",
			NeedsBuild: true,
			RefScheme:  refScheme,
			RepoDir:    "/srv/truffels/data/ckpoolstats",
		},
	}
}

// A build whose image carries a different ref than the one requested must not
// count as an update — this is exactly the failure that used to be invisible.
func TestApplyUpdate_NeedsBuild_LabelMismatchFails(t *testing.T) {
	rec := &needsBuildRecorder{imageLabels: map[string]string{SourceRefLabel: "v1.0.0"}}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("ckpool")
	if err == nil {
		t.Fatal("expected error on label mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected a ref mismatch error, got: %v", err)
	}

	got := rec.snapshot()
	if got.upCalls != 0 {
		t.Errorf("service must not be started when the built ref does not match (up calls=%d)", got.upCalls)
	}

	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if len(logs) == 0 {
		t.Fatal("expected an update log")
	}
	if logs[0].Status != model.UpdateFailed {
		t.Errorf("log status = %v, want %v", logs[0].Status, model.UpdateFailed)
	}
}

func TestApplyUpdate_NeedsBuild_PassesSourceRefBuildArg(t *testing.T) {
	rec := &needsBuildRecorder{imageLabels: map[string]string{SourceRefLabel: "v1.2.0"}}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckpool"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := rec.snapshot()
	if got.buildArgs["SOURCE_REF"] != "v1.2.0" {
		t.Errorf("SOURCE_REF = %q, want v1.2.0", got.buildArgs["SOURCE_REF"])
	}
	// ckpool clones inside its Dockerfile — there is no working copy to move.
	if got.checkoutCalls != 0 {
		t.Errorf("expected no git checkout without RepoDir, got %d calls", got.checkoutCalls)
	}
}

func TestApplyUpdate_NeedsBuild_ChecksOutRepoDirForCommitScheme(t *testing.T) {
	rec := &needsBuildRecorder{imageLabels: map[string]string{SourceRefLabel: "dbd39954"}}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckstatsBuildTemplate(t.TempDir(), model.RefSchemeCommit)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckstats",
		CurrentVersion: "4bccedb",
		LatestVersion:  "dbd39954",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckstats"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := rec.snapshot()
	if got.checkoutDir != "/srv/truffels/data/ckpoolstats" {
		t.Errorf("checkout dir = %q, want the ckstats repo dir", got.checkoutDir)
	}
	if got.checkoutRef != "dbd39954" {
		t.Errorf("checkout ref = %q, want dbd39954", got.checkoutRef)
	}
	if got.checkoutScheme != model.RefSchemeCommit {
		t.Errorf("checkout scheme = %q, want %q", got.checkoutScheme, model.RefSchemeCommit)
	}
}

// An empty RefScheme means "commit" per model.UpdateSource, but the agent reads
// an empty ref_scheme as "tag" and rejects commit hashes. The engine must send
// the model's meaning explicitly rather than pass the blank through.
func TestApplyUpdate_NeedsBuild_EmptyRefSchemeCheckedOutAsCommit(t *testing.T) {
	rec := &needsBuildRecorder{imageLabels: map[string]string{SourceRefLabel: "dbd39954"}}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckstatsBuildTemplate(t.TempDir(), "")
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckstats",
		CurrentVersion: "4bccedb",
		LatestVersion:  "dbd39954",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckstats"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := rec.snapshot().checkoutScheme; got != model.RefSchemeCommit {
		t.Errorf("checkout scheme = %q, want %q for an empty RefScheme", got, model.RefSchemeCommit)
	}
}

// Before this change rollback() only did Down/Up for NeedsBuild services: it
// restarted the very image that had just failed, then reported a rollback.
func TestRollbackRestoresPreviousImage(t *testing.T) {
	rec := &needsBuildRecorder{}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, _ := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	if err := eng.rollback("ckpool", tmpl, tmpl.UpdateSource, "v1.0.0", "v1.2.0"); err != nil {
		t.Fatalf("unexpected rollback error: %v", err)
	}

	got := rec.snapshot()
	if len(got.tags) != 1 {
		t.Fatalf("expected exactly one retag, got %d: %+v", len(got.tags), got.tags)
	}
	if got.tags[0].Source != "truffels/ckpool:rollback" {
		t.Errorf("tag source = %q, want the rollback image", got.tags[0].Source)
	}
	if got.tags[0].Target != "truffels/ckpool:latest" {
		t.Errorf("tag target = %q, want the live tag", got.tags[0].Target)
	}
	if got.upCalls != 1 {
		t.Errorf("service should be started again after the retag, up calls = %d", got.upCalls)
	}
}

// ckpool's compose file pins truffels/ckpool:v1.0.0. Retagging :latest there
// would move a tag nothing runs — the rollback has to target the pinned ref.
func TestRollbackRestoresPinnedComposeTag(t *testing.T) {
	rec := &needsBuildRecorder{}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	composeDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte(`services:
  ckpool:
    build: .
    image: truffels/ckpool:v1.0.0
`), 0644)

	tmpl := ckpoolBuildTemplate(composeDir)
	eng, _ := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	if err := eng.rollback("ckpool", tmpl, tmpl.UpdateSource, "v1.0.0", "v1.2.0"); err != nil {
		t.Fatalf("unexpected rollback error: %v", err)
	}

	got := rec.snapshot()
	if len(got.tags) != 1 || got.tags[0].Target != "truffels/ckpool:v1.0.0" {
		t.Fatalf("expected retag onto the pinned compose ref, got %+v", got.tags)
	}
}

// A rollback that could not restore anything must be reported as such, not
// logged as rolled_back while the broken build keeps running.
func TestRollbackReportsFailureWhenNothingStaged(t *testing.T) {
	rec := &needsBuildRecorder{tagFail: true}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, _ := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	err := eng.rollback("ckpool", tmpl, tmpl.UpdateSource, "v1.0.0", "v1.2.0")
	if err == nil {
		t.Fatal("expected an error when the rollback image cannot be restored")
	}
	if !strings.Contains(err.Error(), "restoring previous image failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// The running image has to be staged before the build overwrites its tag —
// afterwards it is gone.
func TestApplyUpdate_NeedsBuild_StagesRollbackImageBeforeBuild(t *testing.T) {
	rec := &needsBuildRecorder{imageLabels: map[string]string{SourceRefLabel: "v1.2.0"}}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckpool"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := rec.snapshot()
	if len(got.tags) != 1 {
		t.Fatalf("expected one staging retag, got %d: %+v", len(got.tags), got.tags)
	}
	if got.tags[0].Source != "truffels/ckpool:latest" || got.tags[0].Target != "truffels/ckpool:rollback" {
		t.Errorf("staged %q -> %q, want the live tag onto :rollback", got.tags[0].Source, got.tags[0].Target)
	}
}

// checkService does not skip git sources when the container is missing, so the
// apply path has to cope with it too: verification falls back to asking for the
// image by name instead of failing the update outright.
func TestApplyUpdate_NeedsBuild_VerifiesByImageNameWhenContainerMissing(t *testing.T) {
	rec := &needsBuildRecorder{
		inspectFail:  true, // no container to resolve the image through
		byNameLabels: map[string]string{SourceRefLabel: "v1.2.0"},
	}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckpool"); err != nil {
		t.Fatalf("verification should fall back to the image name: %v", err)
	}

	got := rec.snapshot()
	if got.byNameCalls != 1 {
		t.Errorf("expected one inspect-by-name call, got %d", got.byNameCalls)
	}
	if got.byNameImage != "truffels/ckpool:latest" {
		t.Errorf("inspect-by-name asked for %q, want the service's own image", got.byNameImage)
	}

	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if len(logs) == 0 || logs[0].Status != model.UpdateDone {
		t.Errorf("expected a done update log, got %+v", logs)
	}
}

// The fallback must not become a hole in the verification: a wrong label found
// by name still fails the update.
func TestApplyUpdate_NeedsBuild_ByNameLabelMismatchFails(t *testing.T) {
	rec := &needsBuildRecorder{
		inspectFail:  true,
		byNameLabels: map[string]string{SourceRefLabel: "v1.0.0"},
	}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("ckpool")
	if err == nil {
		t.Fatal("expected a mismatch error from the by-name verification")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected a ref mismatch error, got: %v", err)
	}

	got := rec.snapshot()
	if got.upCalls != 0 {
		t.Errorf("service must not start on a mismatch (up calls=%d)", got.upCalls)
	}
}

// Both lookups failing is a real failure — never a silent pass.
func TestApplyUpdate_NeedsBuild_BothInspectRoutesFail(t *testing.T) {
	rec := &needsBuildRecorder{inspectFail: true} // byNameLabels nil -> 500
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("ckpool")
	if err == nil {
		t.Fatal("expected an error when neither inspect route answers")
	}
	if !strings.Contains(err.Error(), "image inspect failed") {
		t.Errorf("unexpected error: %v", err)
	}
	if got := rec.snapshot(); got.upCalls != 0 {
		t.Errorf("service must not start unverified (up calls=%d)", got.upCalls)
	}
}

func TestPruneOldImages_NoLogs(t *testing.T) {
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

	// Should not panic with no logs
	eng.pruneOldImages("electrs", tmpl.UpdateSource)
}
