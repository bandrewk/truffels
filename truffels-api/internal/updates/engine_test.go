package updates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
	"truffels-api/internal/store"
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
	// The staged image must prove it carries the version we are rolling back to.
	agent := newMockAgent(mockAgentOpts{
		tags:        tags,
		imageLabels: map[string]string{SourceRefLabel: "abc123"},
	})
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
	// Staged and labelled, but the retag itself fails (e.g. the image was
	// pruned between inspect and tag).
	agent := newMockAgent(mockAgentOpts{
		tagFail:     true,
		imageLabels: map[string]string{SourceRefLabel: "abc123"},
	})
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

// An image staged before source-ref labels existed (or no image at all — the
// agent answers 200 with empty fields for an unknown image) cannot prove what
// it would restore. Refuse before touching a tag.
func TestRollbackService_RefusesUnverifiableStagedImage(t *testing.T) {
	tags := &tagRecorder{}
	agent := newMockAgent(mockAgentOpts{tags: tags})
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "ckpool", FromVersion: "v1.0.0", ToVersion: "v1.2.0", Status: model.UpdateDone,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "ckpool", CurrentVersion: "v1.2.0", LatestVersion: "v1.2.0",
	})

	err := eng.RollbackService("ckpool")
	if err == nil {
		t.Fatal("expected a refusal for an unlabelled rollback image")
	}
	if !strings.Contains(err.Error(), "no rollback image available") {
		t.Errorf("expected 'no rollback image available', got: %v", err)
	}
	if calls := tags.snapshot(); len(calls) != 0 {
		t.Errorf("nothing may be retagged before the staged image is verified, got %+v", calls)
	}
	if check, _ := st.GetLatestUpdateCheck("ckpool"); check == nil || check.CurrentVersion != "v1.2.0" {
		t.Errorf("version bookkeeping must be untouched, got %+v", check)
	}
}

// :rollback keeps exactly one generation and is only staged by an update. After
// one successful rollback it points at the image that is already running, so a
// second rollback used to retag a no-op, come up healthy and record the version
// it did NOT restore.
func TestRollbackService_SecondRollbackRefusesStaleStagedImage(t *testing.T) {
	tags := &tagRecorder{}
	agent := newMockAgent(mockAgentOpts{
		tags: tags,
		// Still the image staged by the update that ran before the first
		// rollback — i.e. v1.0.0, the version already running.
		imageLabels: map[string]string{SourceRefLabel: "v1.0.0"},
	})
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	// The update, then the rollback that followed it — both successful.
	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "ckpool", FromVersion: "v1.0.0", ToVersion: "v1.2.0", Status: model.UpdateDone,
	})
	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "ckpool", FromVersion: "v1.2.0", ToVersion: "v1.0.0", Status: model.UpdateDone,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "ckpool", CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", HasUpdate: true,
	})

	err := eng.RollbackService("ckpool")
	if err == nil {
		t.Fatal("expected the second rollback to be refused")
	}
	if !strings.Contains(err.Error(), "v1.0.0") || !strings.Contains(err.Error(), "v1.2.0") {
		t.Errorf("error should name both the staged and the requested ref, got: %v", err)
	}
	if calls := tags.snapshot(); len(calls) != 0 {
		t.Errorf("a no-op retag must not happen, got %+v", calls)
	}
	check, _ := st.GetLatestUpdateCheck("ckpool")
	if check == nil || check.CurrentVersion != "v1.0.0" {
		t.Errorf("current version must stay v1.0.0 — that is what runs, got %+v", check)
	}
	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if len(logs) == 0 || logs[0].Status != model.UpdateFailed {
		t.Errorf("expected a failed rollback log, got %+v", logs)
	}
}

// The truffels stack is NeedsBuild but a github_release: it has no staged
// :rollback image and its live image ref cannot be found in its own compose
// file. It must say so instead of failing deep inside the retag path.
func TestRollbackService_SelfUpdateIsRefusedHonestly(t *testing.T) {
	tags := &tagRecorder{}
	agent := newMockAgent(mockAgentOpts{tags: tags})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:             "truffels",
		DisplayName:    "Truffels",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-agent", "truffels-api", "truffels-web"},
		UpdateSource: &model.UpdateSource{
			Type:       model.SourceGitHubRelease,
			Repo:       "bandrewk/truffels",
			Images:     []string{"truffels/agent", "truffels/api", "truffels/web"},
			NeedsBuild: true,
		},
	}
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "truffels",
		FromVersion: "v0.3.1-dev.23",
		ToVersion:   "v0.3.1-dev.24",
		Status:      model.UpdateDone,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "truffels",
		CurrentVersion: "v0.3.1-dev.24",
		LatestVersion:  "v0.3.1-dev.24",
	})

	err := eng.RollbackService("truffels")
	if err == nil {
		t.Fatal("expected rollback of the truffels stack to be refused")
	}
	if !strings.Contains(err.Error(), "rollback not supported for custom-built services") {
		t.Errorf("expected the honest custom-build message, got: %v", err)
	}
	if calls := tags.snapshot(); len(calls) != 0 {
		t.Errorf("the self-update stack must not enter the retag path, got %+v", calls)
	}
	alerts, _ := st.GetActiveAlerts()
	for _, a := range alerts {
		if a.Type == "update_failed" && a.ServiceID == "truffels" {
			t.Errorf("a refused, never-started rollback must not raise an update_failed alert: %+v", a)
		}
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

// A freshly installed ckpool runs the image install.sh built at the Dockerfile's
// default ref, and its compose tag never changes across builds. The image label
// is the only truthful source for "what is running" — reading the tag instead
// left currentVersion empty, and the empty-current branch below then declared
// the newest upstream tag to be the running one ("v1.2.0, up to date" on a box
// running v1.0.0).
func TestCheckService_NeedsBuildUsesImageLabel(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{
		imageLabels: map[string]string{SourceRefLabel: "v1.0.0"},
	})
	defer agent.Close()

	tmpl := ckpoolBuildTemplate(t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	// No update_checks row — the state of a device that was just installed.
	eng.checkService(tmpl)

	check, _ := st.GetLatestUpdateCheck("ckpool")
	if check == nil {
		t.Fatal("expected an update check row for ckpool")
	}
	if check.CurrentVersion != "v1.0.0" {
		t.Errorf("current version = %q, want v1.0.0 from the image label", check.CurrentVersion)
	}
}

// The self-update stack is NeedsBuild too, but carries no source-ref label —
// its version must keep coming from the image tag.
func TestCheckService_SelfUpdateKeepsTagVersion(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := model.ServiceTemplate{
		ID:             "truffels",
		DisplayName:    "Truffels",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-agent"},
		UpdateSource: &model.UpdateSource{
			Type:       model.SourceGitHubRelease,
			Repo:       "bandrewk/truffels",
			Images:     []string{"truffels/agent", "truffels/api", "truffels/web"},
			NeedsBuild: true,
		},
	}
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	eng.checkService(tmpl)

	check, _ := st.GetLatestUpdateCheck("truffels")
	if check == nil {
		t.Fatal("expected an update check row for truffels (the mock image inspect answers with a tag)")
	}
	// The mock's /v1/image/inspect answers "mariadb:lts"; what matters is that
	// the tag is used at all instead of an absent label blanking the row.
	if check.CurrentVersion != "lts" {
		t.Errorf("current version = %q, want the image tag", check.CurrentVersion)
	}
}

// newCommitServer answers the GitHub commits API with a fixed SHA so a
// checkService test can pin latestVersion without touching the network.
func newCommitServer(sha string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": sha})
	}))
}

// A service we build ourselves from a git source states what it is running
// only through org.truffels.source-ref. An image built before label support
// carries none — and the old code then declared the newest upstream commit to
// be the running one. On the device that made ckstats report "up to date"
// while sitting 84 commits (and several security fixes) behind.
func TestCheckService_UnprovableBuildIsNotReportedCurrent(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{}) // no source-ref label on the image
	defer agent.Close()

	srv := newCommitServer("8f2e7c2f1a2b3c4d5e6f")
	defer srv.Close()
	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	tmpl := ckstatsBuildTemplate(t.TempDir(), model.RefSchemeCommit)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	eng.checkService(tmpl)

	check, _ := st.GetLatestUpdateCheck("ckstats")
	if check == nil {
		t.Fatal("expected an update check row for ckstats")
	}
	if check.CurrentVersion != "" {
		t.Errorf("current version = %q, want empty: without the label the running build is unknown", check.CurrentVersion)
	}
	if !check.HasUpdate {
		t.Error("has_update = false; an unprovable build must be offered the rebuild that stamps the label")
	}
	if check.LatestVersion != "8f2e7c2f1a2b" {
		t.Errorf("latest version = %q, want 8f2e7c2f1a2b", check.LatestVersion)
	}
}

// The stored row is the other half of the trap: it was written by the very
// initialisation this change removes, so falling back to it re-reads the lie.
// Nothing else can correct it — the label only appears after an update, and no
// update was ever offered. This is the test that proves the dead end is open.
func TestCheckService_UnprovableBuildIgnoresPoisonedStoredRow(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{}) // still no source-ref label
	defer agent.Close()

	srv := newCommitServer("8f2e7c2f1a2b3c4d5e6f")
	defer srv.Close()
	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	tmpl := ckstatsBuildTemplate(t.TempDir(), model.RefSchemeCommit)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	// Exactly the row measured on the device: current == latest, no update,
	// while the working copy actually sat on an old commit.
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckstats",
		CurrentVersion: "8f2e7c2f1a2b",
		LatestVersion:  "8f2e7c2f1a2b",
		HasUpdate:      false,
	})

	eng.checkService(tmpl)

	check, _ := st.GetLatestUpdateCheck("ckstats")
	if check == nil {
		t.Fatal("expected an update check row for ckstats")
	}
	if check.CurrentVersion != "" {
		t.Errorf("current version = %q, want empty: the stored row is not evidence of what runs", check.CurrentVersion)
	}
	if !check.HasUpdate {
		t.Error("has_update = false; the poisoned row must not keep the service pinned as up to date")
	}
}

// CurrentVersion travels into RollbackService as prevVersion and into the
// update log's FromVersion. A stand-in like "unknown" would be recorded as a
// version that exists and could later be "rolled back to" — so the unknown
// state must stay literally empty, all the way through an apply.
func TestCheckService_UnprovableBuildRecordsNoPlaceholderVersion(t *testing.T) {
	rec := &needsBuildRecorder{} // imageLabels nil: nothing proves what runs
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	srv := newCommitServer("8f2e7c2f1a2b3c4d5e6f")
	defer srv.Close()
	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	composeDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte(`services:
  ckstats:
    image: truffels/ckstats:latest
`), 0644)
	tmpl := ckstatsBuildTemplate(composeDir, model.RefSchemeCommit)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	eng.checkService(tmpl)

	check, _ := st.GetLatestUpdateCheck("ckstats")
	if check == nil {
		t.Fatal("expected an update check row for ckstats")
	}
	for _, banned := range []string{"unknown", "none", "n/a", "-", "8f2e7c2f1a2b"} {
		if check.CurrentVersion == banned {
			t.Fatalf("current version = %q; an unprovable running version must stay empty, not be stood in for", banned)
		}
	}

	// The rebuild the check now offers stamps the label; from here the state heals.
	rec.mu.Lock()
	rec.imageLabels = map[string]string{SourceRefLabel: "8f2e7c2f1a2b"}
	rec.mu.Unlock()

	if err := eng.ApplyUpdate("ckstats"); err != nil {
		t.Fatalf("apply update: %v", err)
	}

	logs, err := st.GetUpdateLogs("ckstats", 10)
	if err != nil || len(logs) == 0 {
		t.Fatalf("expected an update log, got %v (err %v)", logs, err)
	}
	if logs[0].FromVersion != "" {
		t.Errorf("update log from_version = %q, want empty: no invented version may be recorded", logs[0].FromVersion)
	}
	if logs[0].ToVersion != "8f2e7c2f1a2b" {
		t.Errorf("update log to_version = %q, want 8f2e7c2f1a2b", logs[0].ToVersion)
	}

	healed, _ := st.GetLatestUpdateCheck("ckstats")
	if healed.CurrentVersion != "8f2e7c2f1a2b" || healed.HasUpdate {
		t.Errorf("after the update: current = %q has_update = %v, want 8f2e7c2f1a2b / false",
			healed.CurrentVersion, healed.HasUpdate)
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

// checkService now leaves CurrentVersion empty for a build whose image carries
// no source ref. The preflight message must name that state, not render it as
// an empty side of an arrow ("update available:  → 8f2e7c2f"), which reads like
// a formatting bug rather than the deliberate "we do not know" it is.
func TestRunPreflight_UnknownCurrentVersionIsNamed(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	composeDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte(`services:
  ckstats:
    image: truffels/ckstats:latest
`), 0644)

	tmpl := ckstatsBuildTemplate(composeDir, model.RefSchemeCommit)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckstats",
		CurrentVersion: "",
		LatestVersion:  "8f2e7c2f1a2b",
		HasUpdate:      true,
	})

	result, err := eng.RunPreflight("ckstats")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// FromVersion stays empty — it is the honest value and the UI renders its
	// own placeholder. Only the human-readable message changes.
	if result.FromVersion != "" {
		t.Errorf("FromVersion = %q, want empty", result.FromVersion)
	}
	var msg string
	for _, c := range result.Checks {
		if c.Name == "update_available" {
			msg = c.Message
		}
	}
	if msg == "" {
		t.Fatal("no update_available check found")
	}
	if strings.Contains(msg, "available:  ") || strings.Contains(msg, ": →") {
		t.Errorf("message renders the unknown version as a blank: %q", msg)
	}
	if !strings.Contains(msg, "unknown") || !strings.Contains(msg, "source ref") {
		t.Errorf("message = %q, want it to say the running version is unknown and why", msg)
	}
	if !strings.Contains(msg, "8f2e7c2f1a2b") {
		t.Errorf("message = %q, want it to name the target version", msg)
	}
}

// A build-arg default like ARG SOURCE_REF=unknown stamps the literal string
// "unknown" into the label. That is not a ref — it is the absence of one,
// wearing a name. Letting it through made CurrentVersion non-empty, which
// switched off the honest path entirely: it reached update logs, the preflight
// message, the UI and finally the rollback verification, where staged
// "unknown" == prevVersion "unknown" would have approved restoring an image
// nobody can identify.
func TestExtractCurrentVersionFromLabels_PlaceholderIsNotARef(t *testing.T) {
	src := &model.UpdateSource{Type: model.SourceGitHub, Repo: "mrv777/ckstats", NeedsBuild: true}
	for _, labels := range []map[string]string{
		nil,
		{},
		{SourceRefLabel: ""},
		{SourceRefLabel: "unknown"},
		// Our own Dockerfiles only ever emit lowercase, but a label is just a
		// string someone can set — casing must not be a way past the check.
		{SourceRefLabel: "Unknown"},
		{SourceRefLabel: "UNKNOWN"},
		{SourceRefLabel: "  unknown  "},
	} {
		if got := ExtractCurrentVersionFromLabels(src, "truffels/ckstats:latest", labels); got != "" {
			t.Errorf("labels %v: current version = %q, want empty", labels, got)
		}
	}
	// A real ref still comes through untouched.
	real := map[string]string{SourceRefLabel: "8f2e7c2f1a2b"}
	if got := ExtractCurrentVersionFromLabels(src, "truffels/ckstats:latest", real); got != "8f2e7c2f1a2b" {
		t.Errorf("current version = %q, want 8f2e7c2f1a2b", got)
	}
	// ...and a padded one comes back trimmed. The placeholder check already
	// trims before comparing, so returning the raw value would let a ref
	// survive with whitespace that can never equal latestVersion.
	padded := map[string]string{SourceRefLabel: "  8f2e7c2f1a2b\n"}
	if got := ExtractCurrentVersionFromLabels(src, "truffels/ckstats:latest", padded); got != "8f2e7c2f1a2b" {
		t.Errorf("current version = %q, want it trimmed to 8f2e7c2f1a2b", got)
	}
}

// The staged :rollback image is the other place a source-ref label is read. A
// device installed under dev.24 that then took a ckstats update has "unknown"
// in update_log.from_version *and* on the staged image, so staged ==
// prevVersion matched and the rollback ran — writing "unknown" back out as
// to_version and current_version. The restore is physically correct and the
// next check cycle corrects the row, but it is the same lie in the same class,
// and the whole point of this branch is that an unidentifiable image is never
// accepted as proof of a version.
func TestRollbackService_RefusesPlaceholderStagedImage(t *testing.T) {
	rec := &needsBuildRecorder{
		byNameLabels: map[string]string{SourceRefLabel: "unknown"},
	}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	composeDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte(`services:
  ckstats:
    image: truffels/ckstats:latest
`), 0644)

	tmpl := ckstatsBuildTemplate(composeDir, model.RefSchemeCommit)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	// The device history: an update ran from the placeholder-labelled build.
	logID, _ := st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "ckstats", FromVersion: "unknown", ToVersion: "8f2e7c2f1a2b",
		Status: model.UpdatePending,
	})
	_ = st.UpdateLogStatus(logID, model.UpdateDone, "", "")
	// Current version is known, so the empty-version guard is not what fires here.
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckstats",
		CurrentVersion: "8f2e7c2f1a2b",
		LatestVersion:  "8f2e7c2f1a2b",
		HasUpdate:      false,
	})

	err := eng.RollbackService("ckstats")
	if err == nil {
		t.Fatal("expected a refusal: a placeholder label proves nothing about the staged image")
	}
	if !strings.Contains(err.Error(), "no source ref") {
		t.Errorf("error = %q, want it to say the staged image carries no source ref", err.Error())
	}

	// The retag must not have happened — that is the difference between
	// refusing and restoring an image we cannot identify.
	snap := rec.snapshot()
	if len(snap.tags) != 0 {
		t.Errorf("rollback retagged %v despite the placeholder label", snap.tags)
	}
	if snap.upCalls != 0 {
		t.Errorf("rollback restarted the service %d times despite refusing", snap.upCalls)
	}
}

// End to end through checkService: the placeholder must land in the same
// honest state as a missing label, not be stored as a version.
func TestCheckService_PlaceholderLabelIsTreatedAsUnknown(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{
		imageLabels: map[string]string{SourceRefLabel: "unknown"},
	})
	defer agent.Close()

	srv := newCommitServer("8f2e7c2f1a2b3c4d5e6f")
	defer srv.Close()
	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	tmpl := ckstatsBuildTemplate(t.TempDir(), model.RefSchemeCommit)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	eng.checkService(tmpl)

	check, _ := st.GetLatestUpdateCheck("ckstats")
	if check == nil {
		t.Fatal("expected an update check row for ckstats")
	}
	if check.CurrentVersion != "" {
		t.Errorf("current version = %q, want empty: %q is a placeholder, not a ref", check.CurrentVersion, check.CurrentVersion)
	}
	if !check.HasUpdate {
		t.Error("has_update = false; a placeholder label must still offer the rebuild")
	}
}

// The UI hides the rollback button when the running version is unknown, but the
// REST endpoint is reachable directly. Without a server-side guard the function
// ran on ("" != prevVersion), and on success stored LatestVersion:"" with
// HasUpdate:true — which the UI renders as an update with no target and whose
// button then calls ApplyUpdate with an empty SOURCE_REF.
func TestRollbackService_RefusesWhenCurrentVersionUnknown(t *testing.T) {
	rec := &needsBuildRecorder{imageLabels: map[string]string{SourceRefLabel: "8f2e7c2f1a2b"}}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	composeDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte(`services:
  ckstats:
    image: truffels/ckstats:latest
`), 0644)

	tmpl := ckstatsBuildTemplate(composeDir, model.RefSchemeCommit)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	// A completed update exists, so prevVersion resolves...
	logID, _ := st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "ckstats", FromVersion: "4bccedb01234", ToVersion: "8f2e7c2f1a2b",
		Status: model.UpdatePending,
	})
	_ = st.UpdateLogStatus(logID, model.UpdateDone, "", "")
	// ...but what runs now cannot be proven.
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckstats",
		CurrentVersion: "",
		LatestVersion:  "8f2e7c2f1a2b",
		HasUpdate:      true,
	})

	err := eng.RollbackService("ckstats")
	if err == nil {
		t.Fatal("expected a refusal: rolling back from an unknown version is a guess")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error = %q, want it to name the unknown running version", err.Error())
	}

	// Nothing may have been done: no retag, no restart.
	snap := rec.snapshot()
	if len(snap.tags) != 0 {
		t.Errorf("rollback retagged %v despite refusing", snap.tags)
	}
	if snap.upCalls != 0 {
		t.Errorf("rollback restarted the service %d times despite refusing", snap.upCalls)
	}

	// And the stored row must not have become an update with no target.
	check, _ := st.GetLatestUpdateCheck("ckstats")
	if check.HasUpdate && check.LatestVersion == "" {
		t.Error("stored an update with no target version")
	}
}

// Messages that concatenate a version must not break off mid-sentence when the
// version is the deliberate empty "we cannot prove this" state.
func TestVersionLabel_NamesTheUnknownState(t *testing.T) {
	if got := versionLabel("v1.2.0"); got != "v1.2.0" {
		t.Errorf("versionLabel(%q) = %q, want it unchanged", "v1.2.0", got)
	}
	got := versionLabel("")
	if got == "" {
		t.Fatal("versionLabel(\"\") returned empty: the sentence would break off")
	}
	if !strings.Contains(got, "unidentified") && !strings.Contains(got, "unknown") {
		t.Errorf("versionLabel(\"\") = %q, want it to name the unknown state", got)
	}
	// The three call sites that used to trail off.
	for _, msg := range []string{
		"start failed, rolled back to " + versionLabel(""),
		"unhealthy after update, rolled back to " + versionLabel(""),
		fmt.Sprintf("pre-update snapshot (%s → %s)", versionLabel(""), versionLabel("v1.2.0")),
	} {
		if strings.HasSuffix(msg, "to ") || strings.Contains(msg, "( →") {
			t.Errorf("message breaks off mid-clause: %q", msg)
		}
	}
}

// --- pruneOldImages tests ---

func TestPruneOldImages_KeepsCurrentAndN1(t *testing.T) {
	var removedImages []string
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/image/remove":
			var req struct {
				Image string `json:"image"`
			}
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
			var req struct {
				Image string `json:"image"`
			}
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
	byNameByRef    map[string]map[string]string
	byNameImage    string // ref the engine asked for by name
	byNameRefs     []string
	byNameCalls    int
	tagFail        bool // /v1/image/tag 500s — nothing staged to restore
	tags           []tagCall
	removed        []string // images dropped via /v1/image/remove
	unhealthy      bool     // /v1/inspect reports the containers unhealthy
	buildFail      bool     // /v1/compose/build 500s
	buildArgs      map[string]string
	checkoutDir    string
	checkoutRef    string
	checkoutScheme string
	checkoutCalls  int
	upCalls        int
	// composeDirs maps service_id -> compose dir, mirroring the agent's own
	// service_id -> /srv/truffels/compose/<id> mapping. A service registered
	// here gets its docker-compose.yml really rewritten by the mock; one that is
	// not registered gets the answer the agent gives for a file it cannot read.
	composeDirs  map[string]string
	rewriteFail  bool
	rewrites     []rewriteCall
	rewriteCalls int
	// events is the ordered trace of everything the engine asked for, so a test
	// can assert that staging happened *before* the tag was rewritten rather
	// than merely that both happened.
	events []string
}

// rewriteCall is one /v1/compose/rewrite-tags request.
type rewriteCall struct {
	ServiceID string   `json:"service_id"`
	Images    []string `json:"images"`
	OldTag    string   `json:"old_tag"`
	NewTag    string   `json:"new_tag"`
}

func (r *needsBuildRecorder) snapshot() needsBuildRecorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	return needsBuildRecorder{
		byNameImage:    r.byNameImage,
		byNameRefs:     append([]string(nil), r.byNameRefs...),
		byNameCalls:    r.byNameCalls,
		tags:           append([]tagCall(nil), r.tags...),
		removed:        append([]string(nil), r.removed...),
		buildArgs:      r.buildArgs,
		checkoutDir:    r.checkoutDir,
		checkoutRef:    r.checkoutRef,
		checkoutScheme: r.checkoutScheme,
		checkoutCalls:  r.checkoutCalls,
		upCalls:        r.upCalls,
		rewrites:       append([]rewriteCall(nil), r.rewrites...),
		rewriteCalls:   r.rewriteCalls,
		events:         append([]string(nil), r.events...),
	}
}

// event appends to the ordered trace. Caller must hold the lock.
func (r *needsBuildRecorder) event(format string, args ...any) {
	r.events = append(r.events, fmt.Sprintf(format, args...))
}

// indexOf returns the position of the first event with the given prefix, or -1.
// Called on a snapshot, which nothing else holds — the pointer receiver is only
// there to avoid copying the recorder's mutex.
func (r *needsBuildRecorder) indexOf(prefix string) int {
	for i, e := range r.events {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
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
			rec.event("build %s", req.BuildArgs["SOURCE_REF"])
			fail := rec.buildFail
			rec.mu.Unlock()
			if fail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "build failed"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		// Mirrors handleComposeRewriteTags: the agent ignores old_tag and
		// matches whatever tag the file currently carries, refuses when nothing
		// matched, and 500s when it cannot read the file at all.
		case "/v1/compose/rewrite-tags":
			var req rewriteCall
			_ = json.NewDecoder(r.Body).Decode(&req)
			rec.mu.Lock()
			rec.rewrites = append(rec.rewrites, req)
			rec.rewriteCalls++
			rec.event("rewrite %s->%s", req.OldTag, req.NewTag)
			dir, known := rec.composeDirs[req.ServiceID]
			fail := rec.rewriteFail
			rec.mu.Unlock()
			if fail {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "write compose file: read-only file system"})
				return
			}
			if !known {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "read compose file: no such file or directory"})
				return
			}
			composePath := filepath.Join(dir, "docker-compose.yml")
			data, readErr := os.ReadFile(composePath)
			if readErr != nil {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "read compose file: " + readErr.Error()})
				return
			}
			content, matched := string(data), 0
			for _, img := range req.Images {
				re := regexp.MustCompile(fmt.Sprintf(`(image:\s*)%s:[^\s@]+(@sha256:[a-f0-9]+)?`, regexp.QuoteMeta(img)))
				if re.MatchString(content) {
					matched++
				}
				content = re.ReplaceAllString(content, fmt.Sprintf("${1}%s:%s", img, req.NewTag))
			}
			if matched == 0 {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "no image tags matched in compose file"})
				return
			}
			_ = os.WriteFile(composePath, []byte(content), 0644)
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
			rec.byNameRefs = append(rec.byNameRefs, req.Image)
			rec.byNameCalls++
			rec.event("inspect-by-name %s", req.Image)
			labels := rec.byNameLabels
			// byNameByRef answers per ref, so a test can hand the newly built
			// tag one label and the ref the old container still names another.
			if byRef, ok := rec.byNameByRef[req.Image]; ok {
				labels = byRef
			} else if rec.byNameByRef != nil {
				labels = nil
			}
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
			rec.event("tag %s->%s", req.Source, req.Target)
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
			rec.event("up")
			rec.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		// A custom build has no registry behind it. Recording pulls proves the
		// populated Images list did not divert the apply path into the
		// registry branch that pulls each image by tag.
		case "/v1/image/pull":
			var req struct {
				Image string `json:"image"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			rec.mu.Lock()
			rec.event("pull %s", req.Image)
			rec.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/image/remove":
			var req struct {
				Image string `json:"image"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			rec.mu.Lock()
			rec.removed = append(rec.removed, req.Image)
			rec.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/inspect":
			var req struct {
				Containers []string `json:"containers"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			rec.mu.Lock()
			health := "healthy"
			if rec.unhealthy {
				health = "unhealthy"
			}
			rec.mu.Unlock()
			var states []map[string]interface{}
			for _, name := range req.Containers {
				states = append(states, map[string]interface{}{
					"name": name, "status": "running", "health": health,
				})
			}
			_ = json.NewEncoder(w).Encode(states)

		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
}

// The Images list mirrors templates.Ckpool: it is what names the compose lines
// the update path retags, and a fixture without it would never catch the field
// going missing again.
func ckpoolBuildTemplate(composeDir string) model.ServiceTemplate {
	return model.ServiceTemplate{
		ID:             "ckpool",
		DisplayName:    "ckpool",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-ckpool"},
		UpdateSource: &model.UpdateSource{
			Type:       model.SourceBitbucket,
			Repo:       "ckolivas/ckpool",
			Images:     []string{"truffels/ckpool"},
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
			Images:     []string{"truffels/ckstats"},
			Branch:     "main",
			NeedsBuild: true,
			RefScheme:  refScheme,
			RepoDir:    "/srv/truffels/data/ckpoolstats",
		},
	}
}

// buildComposeDir lays down the compose file a custom-built service really has
// on disk — a build block plus the pinned image line the build tags its output
// with — and returns the directory holding it.
func buildComposeDir(t *testing.T, serviceID, tag string) string {
	t.Helper()
	dir := t.TempDir()
	body := fmt.Sprintf(`services:
  %[1]s:
    build:
      context: /srv/truffels/compose/%[1]s
    image: truffels/%[1]s:%[2]s
    container_name: truffels-%[1]s
`, serviceID, tag)
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(body), 0644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	return dir
}

// composeImageTag reads back the tag the compose file in dir names for the
// service's own image — the statement `docker ps` and the file itself make.
func composeImageTag(t *testing.T, dir, serviceID string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read compose: %v", err)
	}
	m := regexp.MustCompile(`image:\s*truffels/` + regexp.QuoteMeta(serviceID) + `:(\S+)`).FindStringSubmatch(string(data))
	if m == nil {
		t.Fatalf("no truffels/%s image line in:\n%s", serviceID, string(data))
	}
	return m[1]
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

// A failed staging must not leave the previous update's :rollback tag in place:
// restoring that later would install a two-generations-old image and report a
// successful rollback.
func TestApplyUpdate_NeedsBuild_FailedStagingDropsStaleRollbackTag(t *testing.T) {
	rec := &needsBuildRecorder{
		tagFail:     true, // staging retag fails; :rollback still points at N-2
		imageLabels: map[string]string{SourceRefLabel: "v1.2.0"},
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

	// Staging is best-effort — a failure there must not block the update.
	if err := eng.ApplyUpdate("ckpool"); err != nil {
		t.Fatalf("failed staging must not block the update: %v", err)
	}

	got := rec.snapshot()
	found := false
	for _, img := range got.removed {
		if img == "truffels/ckpool:rollback" {
			found = true
		}
	}
	if !found {
		t.Errorf("stale rollback tag was not dropped, removed = %v", got.removed)
	}
}

// The same run end to end: staging fails, the service comes up unhealthy, and
// the rollback has nothing valid to restore. It must say so — the old code
// logged rolled_back while the failed build kept running.
func TestApplyUpdate_NeedsBuild_StaleStagingRollbackFailsHonestly(t *testing.T) {
	rec := &needsBuildRecorder{
		tagFail:     true,
		unhealthy:   true,
		imageLabels: map[string]string{SourceRefLabel: "v1.2.0"},
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
		t.Fatal("expected an error when the service is unhealthy and cannot be rolled back")
	}
	if !strings.Contains(err.Error(), "rollback incomplete") {
		t.Errorf("expected an honest 'rollback incomplete' error, got: %v", err)
	}

	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if len(logs) == 0 {
		t.Fatal("expected an update log")
	}
	if logs[0].Status == model.UpdateRolledBack {
		t.Error("log claims rolled_back although nothing was restored")
	}
	if logs[0].Status != model.UpdateFailed {
		t.Errorf("log status = %v, want %v", logs[0].Status, model.UpdateFailed)
	}

	got := rec.snapshot()
	found := false
	for _, img := range got.removed {
		if img == "truffels/ckpool:rollback" {
			found = true
		}
	}
	if !found {
		t.Errorf("stale rollback tag was not dropped, removed = %v", got.removed)
	}
}

// The :latest fallback is a misconfiguration, not a normal case — it must leave
// a trace, otherwise a wrong live ref only surfaces when a rollback needs it.
func TestLiveImageRef_FallbackIsLogged(t *testing.T) {
	cases := []struct {
		name    string
		compose string // empty means: write no compose file at all
		wantLog string
	}{
		{"interpolated tag", "services:\n  ckpool:\n    image: truffels/ckpool:${TAG}\n", "no plain truffels image line"},
		{"trailing comment", "services:\n  ckpool:\n    image: truffels/ckpool:v1.0.0 # pinned\n", "no plain truffels image line"},
		{"unreadable compose file", "", "cannot read compose file"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			dir := t.TempDir()
			if tc.compose != "" {
				_ = os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(tc.compose), 0644)
			}

			got := liveImageRef(ckpoolBuildTemplate(dir), "ckpool")
			if got != "truffels/ckpool:latest" {
				t.Errorf("live ref = %q, want the conventional fallback", got)
			}
			if !strings.Contains(buf.String(), tc.wantLog) {
				t.Errorf("expected a warning containing %q, got: %s", tc.wantLog, buf.String())
			}
			if !strings.Contains(buf.String(), "ckpool") {
				t.Errorf("warning does not name the service: %s", buf.String())
			}
		})
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

// --- Compose tag follows the build (ckpool / ckstats) ---
//
// The tag a custom-built service runs under is a statement about its contents:
// it is what `docker ps` shows and what the compose file says. ckpool shipped
// for six releases as truffels/ckpool:v1.0.0 while the image behind that name
// carried source-ref v1.2.0, because the update path never retagged it. These
// tests hold the tag to the build.

// needsBuildEngine wires a ckpool template whose compose file the mock really
// rewrites, the way the agent does on the device.
func needsBuildEngine(t *testing.T, rec *needsBuildRecorder, serviceID, tag string) (*Engine, *store.Store, model.ServiceTemplate, string) {
	t.Helper()
	dir := buildComposeDir(t, serviceID, tag)
	if rec.composeDirs == nil {
		rec.composeDirs = map[string]string{}
	}
	rec.composeDirs[serviceID] = dir
	agent := newNeedsBuildAgent(rec)
	t.Cleanup(agent.Close)

	tmpl := ckpoolBuildTemplate(dir)
	if serviceID != "ckpool" {
		tmpl = ckstatsBuildTemplate(dir, model.RefSchemeCommit)
	}
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})
	return eng, st, tmpl, dir
}

func TestApplyUpdate_NeedsBuild_ComposeTagFollowsTheBuild(t *testing.T) {
	rec := &needsBuildRecorder{
		// The old container still references truffels/ckpool:v1.0.0, and that
		// image carries the old ref. Verifying through the container would read
		// exactly this and fail a build that is in fact correct.
		imageLabels: map[string]string{SourceRefLabel: "v1.0.0"},
		byNameByRef: map[string]map[string]string{
			"truffels/ckpool:v1.2.0": {SourceRefLabel: "v1.2.0"},
		},
	}
	eng, st, _, dir := needsBuildEngine(t, rec, "ckpool", "v1.0.0")

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckpool"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := composeImageTag(t, dir, "ckpool"); got != "v1.2.0" {
		t.Errorf("compose file names truffels/ckpool:%s, want the version that was just built", got)
	}

	got := rec.snapshot()
	if got.byNameImage != "truffels/ckpool:v1.2.0" {
		t.Errorf("verification asked for %q, want the ref the compose file now names", got.byNameImage)
	}
	if got.upCalls != 1 {
		t.Errorf("expected the service to be restarted once, up calls = %d", got.upCalls)
	}
	// Filling in Images must not divert a custom build into the registry
	// branch: there is no truffels/ckpool on any registry to pull.
	if i := got.indexOf("pull "); i >= 0 {
		t.Errorf("a custom build must never pull from a registry: %v", got.events)
	}

	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if len(logs) == 0 || logs[0].Status != model.UpdateDone {
		t.Fatalf("expected a done update log, got %+v", logs)
	}
}

// The rollback image is staged from the ref the compose file names, so staging
// has to happen before the rewrite moves that name. Afterwards it would tag an
// image that does not exist yet and the rollback would be silently useless.
func TestApplyUpdate_NeedsBuild_StagesRollbackBeforeTheRewrite(t *testing.T) {
	rec := &needsBuildRecorder{
		byNameByRef: map[string]map[string]string{
			"truffels/ckpool:v1.2.0": {SourceRefLabel: "v1.2.0"},
		},
	}
	eng, st, _, _ := needsBuildEngine(t, rec, "ckpool", "v1.0.0")

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
	staged := got.indexOf("tag truffels/ckpool:v1.0.0->truffels/ckpool:rollback")
	rewritten := got.indexOf("rewrite ")
	built := got.indexOf("build ")
	if staged < 0 {
		t.Fatalf("the running image was never staged from its own ref: %v", got.events)
	}
	if rewritten < 0 || built < 0 {
		t.Fatalf("expected a rewrite and a build, got: %v", got.events)
	}
	if staged > rewritten {
		t.Errorf("staged after the tag was rewritten — :rollback would point at nothing: %v", got.events)
	}
	if rewritten > built {
		t.Errorf("rewrote after the build — the build would carry the old tag: %v", got.events)
	}
}

// A build that cannot be shown to hold the requested ref must leave nothing
// behind that says it does. The container is not restarted, but the compose
// file would still name a version whose image is the failed build, and the next
// restart from any other cause would pull it up.
func TestApplyUpdate_NeedsBuild_FailedVerificationPutsTheComposeTagBack(t *testing.T) {
	rec := &needsBuildRecorder{
		byNameByRef: map[string]map[string]string{
			"truffels/ckpool:v1.2.0": {SourceRefLabel: "v1.0.0"}, // build produced the old code
		},
	}
	eng, st, _, dir := needsBuildEngine(t, rec, "ckpool", "v1.0.0")

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("ckpool")
	if err == nil {
		t.Fatal("expected the mismatched build to fail the update")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("unexpected error: %v", err)
	}

	if got := composeImageTag(t, dir, "ckpool"); got != "v1.0.0" {
		t.Errorf("compose file left at truffels/ckpool:%s — it names a version whose image is a failed build", got)
	}
	if got := rec.snapshot(); got.upCalls != 0 {
		t.Errorf("service must not be restarted on a failed verification (up calls=%d)", got.upCalls)
	}
}

func TestApplyUpdate_NeedsBuild_FailedBuildPutsTheComposeTagBack(t *testing.T) {
	rec := &needsBuildRecorder{buildFail: true}
	eng, st, _, dir := needsBuildEngine(t, rec, "ckpool", "v1.0.0")

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("ckpool")
	if err == nil {
		t.Fatal("expected the failed build to fail the update")
	}
	if !strings.Contains(err.Error(), "build failed") {
		t.Errorf("unexpected error: %v", err)
	}
	if got := composeImageTag(t, dir, "ckpool"); got != "v1.0.0" {
		t.Errorf("compose file left at truffels/ckpool:%s after a failed build", got)
	}
}

// The tag is a claim, the source-ref label is the proof — so a compose file the
// agent will not rewrite must not block an update that otherwise works. It only
// leaves the tag as stale as it already was, and the engine has to notice that
// and keep verifying the ref the file still names.
func TestApplyUpdate_NeedsBuild_RewriteFailureKeepsTheOldTagAndStillUpdates(t *testing.T) {
	rec := &needsBuildRecorder{
		rewriteFail: true,
		byNameByRef: map[string]map[string]string{
			"truffels/ckpool:v1.0.0": {SourceRefLabel: "v1.2.0"}, // built under the old tag
		},
	}
	eng, st, _, dir := needsBuildEngine(t, rec, "ckpool", "v1.0.0")

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckpool"); err != nil {
		t.Fatalf("a failed retag must not block the update: %v", err)
	}
	if got := composeImageTag(t, dir, "ckpool"); got != "v1.0.0" {
		t.Errorf("compose tag = %s, want the untouched old tag", got)
	}
	if got := rec.snapshot(); got.byNameImage != "truffels/ckpool:v1.0.0" {
		t.Errorf("verification asked for %q, want the ref the file still names", got.byNameImage)
	}
}

// The device state this fix was written for: the compose file says v1.0.0 while
// the image it names carries v1.2.0, so the update check reports v1.2.0 as the
// current version. The old tag has to come off the file, not off the check —
// otherwise the rewrite reasons about a tag that is not there.
func TestApplyUpdate_NeedsBuild_OldTagComesFromTheComposeFile(t *testing.T) {
	rec := &needsBuildRecorder{
		byNameByRef: map[string]map[string]string{
			"truffels/ckpool:v1.3.0": {SourceRefLabel: "v1.3.0"},
		},
	}
	eng, st, _, dir := needsBuildEngine(t, rec, "ckpool", "v1.0.0")

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.2.0", // the source-ref label, not the tag
		LatestVersion:  "v1.3.0",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckpool"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := rec.snapshot()
	if len(got.rewrites) == 0 {
		t.Fatal("expected a rewrite call")
	}
	if got.rewrites[0].OldTag != "v1.0.0" {
		t.Errorf("old tag = %q, want the tag the compose file actually carries", got.rewrites[0].OldTag)
	}
	if got.rewrites[0].NewTag != "v1.3.0" {
		t.Errorf("new tag = %q, want the version being built", got.rewrites[0].NewTag)
	}
	if got.tags[0].Source != "truffels/ckpool:v1.0.0" {
		t.Errorf("staged from %q, want the ref the compose file names", got.tags[0].Source)
	}
	if tag := composeImageTag(t, dir, "ckpool"); tag != "v1.3.0" {
		t.Errorf("compose tag = %s, want v1.3.0", tag)
	}
}

// A rollback has to undo the rewrite as well. Leaving the new tag in the file
// while restoring the old image would put the lie back, one generation on.
func TestApplyUpdate_NeedsBuild_UnhealthyRollbackPutsTheComposeTagBack(t *testing.T) {
	rec := &needsBuildRecorder{
		unhealthy: true,
		byNameByRef: map[string]map[string]string{
			"truffels/ckpool:v1.2.0": {SourceRefLabel: "v1.2.0"},
		},
	}
	eng, st, _, dir := needsBuildEngine(t, rec, "ckpool", "v1.0.0")

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("ckpool")
	if err == nil {
		t.Fatal("expected an error when the service comes up unhealthy")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("unexpected error: %v", err)
	}

	if got := composeImageTag(t, dir, "ckpool"); got != "v1.0.0" {
		t.Errorf("compose tag = %s after the rollback, want the version that is running again", got)
	}

	got := rec.snapshot()
	last := got.tags[len(got.tags)-1]
	if last.Source != "truffels/ckpool:rollback" || last.Target != "truffels/ckpool:v1.0.0" {
		t.Errorf("restored %q -> %q, want the staged image onto the restored tag", last.Source, last.Target)
	}

	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if len(logs) == 0 || logs[0].Status != model.UpdateRolledBack {
		t.Fatalf("expected a rolled_back log, got %+v", logs)
	}
}

// Same for the manual rollback: the file has to name the version that runs
// after it, not the one it left.
func TestRollbackService_CustomBuildMovesTheComposeTagBack(t *testing.T) {
	rec := &needsBuildRecorder{
		byNameLabels: map[string]string{SourceRefLabel: "v1.0.0"}, // what :rollback holds
	}
	eng, st, _, dir := needsBuildEngine(t, rec, "ckpool", "v1.2.0")

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "ckpool", FromVersion: "v1.0.0", ToVersion: "v1.2.0", Status: model.UpdateDone,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "ckpool", CurrentVersion: "v1.2.0", LatestVersion: "v1.2.0",
	})

	if err := eng.RollbackService("ckpool"); err != nil {
		t.Fatalf("unexpected rollback error: %v", err)
	}

	if got := composeImageTag(t, dir, "ckpool"); got != "v1.0.0" {
		t.Errorf("compose tag = %s after the rollback, want v1.0.0", got)
	}

	got := rec.snapshot()
	if len(got.tags) != 1 {
		t.Fatalf("expected exactly one retag, got %+v", got.tags)
	}
	if got.tags[0].Target != "truffels/ckpool:v1.0.0" {
		t.Errorf("restored onto %q, want the ref the compose file now names", got.tags[0].Target)
	}
	// Order matters here too: a retag that fails must leave the file alone.
	if got.indexOf("tag ") > got.indexOf("rewrite ") {
		t.Errorf("rewrote the compose file before the image was restored: %v", got.events)
	}
}

// ckstats runs the same image in two compose services (the app and its cron).
// One entry in Images has to move both lines — a half-rewritten file would
// start one container on an image that no longer exists.
func TestApplyUpdate_NeedsBuild_RewritesEveryImageLine(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yml")
	_ = os.WriteFile(composePath, []byte(`services:
  ckstats:
    build:
      context: /srv/truffels/data
    image: truffels/ckstats:latest
    container_name: truffels-ckstats
  ckstats-cron:
    image: truffels/ckstats:latest
    container_name: truffels-ckstats-cron
  ckstats-db:
    image: postgres:16.14-alpine
`), 0644)

	rec := &needsBuildRecorder{
		composeDirs: map[string]string{"ckstats": dir},
		byNameByRef: map[string]map[string]string{
			"truffels/ckstats:8f2e7c2f8403": {SourceRefLabel: "8f2e7c2f8403"},
		},
	}
	agent := newNeedsBuildAgent(rec)
	defer agent.Close()

	tmpl := ckstatsBuildTemplate(dir, model.RefSchemeCommit)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckstats",
		CurrentVersion: "c9bf72e4cb6e",
		LatestVersion:  "8f2e7c2f8403",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckstats"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(composePath)
	if n := strings.Count(string(data), "image: truffels/ckstats:8f2e7c2f8403"); n != 2 {
		t.Errorf("expected both ckstats image lines rewritten, got %d:\n%s", n, string(data))
	}
	if strings.Contains(string(data), "truffels/ckstats:latest") {
		t.Errorf("a stale :latest line survived:\n%s", string(data))
	}
	// The database is not ours to retag.
	if !strings.Contains(string(data), "image: postgres:16.14-alpine") {
		t.Errorf("the postgres image line was touched:\n%s", string(data))
	}
}

// Filling in Images also switches pruning on for the custom builds — it never
// removed anything for them before, because there was no image name to remove.
// The rule it now applies has to spare both the image that is running and the
// one generation staged for rollback.
func TestPruneOldImages_NeedsBuildSparesRunningAndRollback(t *testing.T) {
	rec := &needsBuildRecorder{}
	eng, st, tmpl, _ := needsBuildEngine(t, rec, "ckpool", "v1.3.0")

	// ckpool's real history on the device: commit-scheme updates, then the
	// switch to tags, then the update that just landed.
	for _, l := range []model.UpdateLog{
		{ServiceID: "ckpool", FromVersion: "7f1a7d573699", ToVersion: "481f4cfe348e", Status: model.UpdateDone},
		{ServiceID: "ckpool", FromVersion: "481f4cfe348e", ToVersion: "2e44101e2da2", Status: model.UpdateDone},
		{ServiceID: "ckpool", FromVersion: "v1.2.0", ToVersion: "v1.3.0", Status: model.UpdateDone},
	} {
		_, _ = st.CreateUpdateLog(&l)
	}

	eng.pruneOldImages("ckpool", tmpl.UpdateSource)

	got := rec.snapshot()
	for _, img := range got.removed {
		switch img {
		case "truffels/ckpool:v1.3.0":
			t.Errorf("prune removed the tag the compose file runs: %v", got.removed)
		case "truffels/ckpool:v1.2.0":
			t.Errorf("prune removed the rollback generation: %v", got.removed)
		case "truffels/ckpool:rollback":
			t.Errorf("prune removed the staged rollback tag: %v", got.removed)
		}
	}
	// It does clean up the generations before those two.
	if len(got.removed) == 0 {
		t.Error("expected the older generations to be pruned now that Images is set")
	}
}

// The agent answers a ref it cannot resolve with a 200 and empty fields, and
// the :latest fallback looks exactly the same from here. Verification must then
// still try the container route rather than fail a correct build — and the
// container's label still has to match, so this cannot pass anything through.
func TestApplyUpdate_NeedsBuild_EmptyByNameFallsBackToTheContainer(t *testing.T) {
	rec := &needsBuildRecorder{
		imageLabels: map[string]string{SourceRefLabel: "v1.2.0"}, // the container route
		byNameByRef: map[string]map[string]string{
			"truffels/ckpool:v1.2.0": {}, // resolved, but carries no label
		},
	}
	eng, st, _, _ := needsBuildEngine(t, rec, "ckpool", "v1.0.0")

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	if err := eng.ApplyUpdate("ckpool"); err != nil {
		t.Fatalf("verification should fall back to the container: %v", err)
	}
	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if len(logs) == 0 || logs[0].Status != model.UpdateDone {
		t.Fatalf("expected a done update log, got %+v", logs)
	}
}

// ...and when neither route produces a ref, the update fails with the honest
// "nothing proves what this image holds" message instead of starting it.
func TestApplyUpdate_NeedsBuild_NoLabelAnywhereFails(t *testing.T) {
	rec := &needsBuildRecorder{
		imageLabels: map[string]string{},
		byNameByRef: map[string]map[string]string{
			"truffels/ckpool:v1.2.0": {},
		},
	}
	eng, st, _, dir := needsBuildEngine(t, rec, "ckpool", "v1.0.0")

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "v1.0.0",
		LatestVersion:  "v1.2.0",
		HasUpdate:      true,
	})

	err := eng.ApplyUpdate("ckpool")
	if err == nil {
		t.Fatal("expected an unverifiable build to fail the update")
	}
	if !strings.Contains(err.Error(), `does not match`) {
		t.Errorf("unexpected error: %v", err)
	}
	if got := rec.snapshot(); got.upCalls != 0 {
		t.Errorf("service must not start unverified (up calls=%d)", got.upCalls)
	}
	if got := composeImageTag(t, dir, "ckpool"); got != "v1.0.0" {
		t.Errorf("compose tag = %s, want the old tag restored", got)
	}
}
