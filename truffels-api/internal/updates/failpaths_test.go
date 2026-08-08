package updates

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"truffels-api/internal/model"
	"truffels-api/internal/store"
)

// These tests pin the failure paths of the update engine that write an update
// log, raise the update_failed alert and return an error. All three belong
// together — a path that logs but forgets the alert leaves the UI quiet about a
// broken service — so every case asserts all three.

// assertFailedWithAlert checks the three things a failed step must produce: the
// returned error, the failed update log with its message, and the active alert.
func assertFailedWithAlert(t *testing.T, st *store.Store, serviceID string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("expected %q in returned error, got: %v", want, err)
	}

	logs, _ := st.GetUpdateLogs(serviceID, 5)
	if len(logs) == 0 {
		t.Fatalf("expected an update log for %s", serviceID)
	}
	if logs[0].Status != model.UpdateFailed {
		t.Errorf("expected status failed, got %s", logs[0].Status)
	}
	if !strings.Contains(logs[0].Error, want) {
		t.Errorf("expected %q in the update log error, got: %s", want, logs[0].Error)
	}

	alerts, _ := st.GetActiveAlerts()
	found := false
	for _, a := range alerts {
		if a.Type == "update_failed" && a.ServiceID == serviceID {
			found = true
			if a.Severity != model.SeverityCritical {
				t.Errorf("expected a critical alert, got %s", a.Severity)
			}
		}
	}
	if !found {
		t.Errorf("expected an active update_failed alert for %s, got %+v", serviceID, alerts)
	}
}

// mempoolTemplate is a plain registry-pulled service: no build, no floating tag.
func mempoolTemplate(t *testing.T, composeDir string) model.ServiceTemplate {
	t.Helper()
	body := `services:
  backend:
    image: mempool/backend:v3.2.0
`
	if err := os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte(body), 0644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	return model.ServiceTemplate{
		ID:             "mempool",
		DisplayName:    "mempool.space",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-mempool-backend"},
		UpdateSource: &model.UpdateSource{
			Type:   model.SourceDockerHub,
			Images: []string{"mempool/backend"},
		},
	}
}

// ckpoolTemplate is a custom-built service. repoDir empty means the build clones
// inside its own Dockerfile and no git checkout happens.
func ckpoolTemplate(t *testing.T, composeDir, repoDir string) model.ServiceTemplate {
	t.Helper()
	body := `services:
  ckpool:
    build: .
    image: truffels/ckpool:v1.0.0
`
	if err := os.WriteFile(filepath.Join(composeDir, "docker-compose.yml"), []byte(body), 0644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	return model.ServiceTemplate{
		ID:             "ckpool",
		DisplayName:    "ckpool",
		ComposeDir:     composeDir,
		ContainerNames: []string{"truffels-ckpool"},
		UpdateSource: &model.UpdateSource{
			Type:       model.SourceBitbucket,
			Repo:       "ckolivas/ckpool",
			Branch:     "master",
			RepoDir:    repoDir,
			NeedsBuild: true,
		},
	}
}

// --- ApplyUpdate: standard (pull) failures ---

func TestApplyUpdate_Standard_ComposeRewriteFails(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{rewriteFail: true})
	defer agent.Close()

	tmpl := mempoolTemplate(t, t.TempDir())
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "mempool", CurrentVersion: "v3.2.0", LatestVersion: "v3.2.1", HasUpdate: true,
	})

	err := eng.ApplyUpdate("mempool")
	assertFailedWithAlert(t, st, "mempool", err, "compose rewrite failed")
}

func TestApplyUpdate_Standard_StopFails(t *testing.T) {
	cd := t.TempDir()
	agent := newMockAgent(mockAgentOpts{
		downFail:    true,
		composeDirs: map[string]string{"mempool": cd},
	})
	defer agent.Close()

	tmpl := mempoolTemplate(t, cd)
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "mempool", CurrentVersion: "v3.2.0", LatestVersion: "v3.2.1", HasUpdate: true,
	})

	err := eng.ApplyUpdate("mempool")
	assertFailedWithAlert(t, st, "mempool", err, "stop failed")

	// The version to go back to is recorded on the log, not left empty.
	logs, _ := st.GetUpdateLogs("mempool", 5)
	if logs[0].RollbackVersion != "v3.2.0" {
		t.Errorf("expected rollback_version v3.2.0, got %q", logs[0].RollbackVersion)
	}
}

// --- ApplyUpdate: custom build failures ---

func TestApplyUpdate_BuildService_GitCheckoutFails(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{gitCheckoutFail: true})
	defer agent.Close()

	tmpl := ckpoolTemplate(t, t.TempDir(), "/repo/ckpool")
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "ckpool", CurrentVersion: "abc123def456", LatestVersion: "def789abc012", HasUpdate: true,
	})

	err := eng.ApplyUpdate("ckpool")
	assertFailedWithAlert(t, st, "ckpool", err, "git checkout failed")
}

// The build succeeds, the restart does not, and the rollback cannot put the old
// image back either — the caller must say so rather than claim a rollback.
func TestApplyUpdate_BuildService_StartFails_RollbackIncomplete(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{
		upFail:      true,
		tagFail:     true,
		imageLabels: map[string]string{SourceRefLabel: "def789abc012"},
	})
	defer agent.Close()

	tmpl := ckpoolTemplate(t, t.TempDir(), "")
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "ckpool", CurrentVersion: "abc123def456", LatestVersion: "def789abc012", HasUpdate: true,
	})

	err := eng.ApplyUpdate("ckpool")
	assertFailedWithAlert(t, st, "ckpool", err, "rollback incomplete")

	logs, _ := st.GetUpdateLogs("ckpool", 5)
	if !strings.Contains(logs[0].Error, "start failed") {
		t.Errorf("expected the start failure to survive into the log, got: %s", logs[0].Error)
	}
	if logs[0].RollbackVersion != "abc123def456" {
		t.Errorf("expected rollback_version abc123def456, got %q", logs[0].RollbackVersion)
	}
}

// --- RollbackService ---

// The staged image cannot even be inspected: refuse before moving any tag.
func TestRollbackService_StagedImageInspectFails(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{imageInspectFail: true})
	defer agent.Close()

	tmpl := ckpoolTemplate(t, t.TempDir(), "")
	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})

	_, _ = st.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "ckpool", FromVersion: "abc123", ToVersion: "def456", Status: model.UpdateDone,
	})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "ckpool", CurrentVersion: "def456", LatestVersion: "def456",
	})

	err := eng.RollbackService("ckpool")
	assertFailedWithAlert(t, st, "ckpool", err, "cannot verify the staged rollback image")
}

// --- Self-update ---

func TestApplySelfUpdate_GitCheckoutFails(t *testing.T) {
	cd := t.TempDir()
	agent := newSelfUpdateMockAgent(mockAgentOpts{
		composeDirs:     map[string]string{"truffels": cd},
		gitCheckoutFail: true,
	})
	defer agent.Close()

	composePath := writeSelfUpdateCompose(t, cd, "v0.1.0")
	eng, st := newTestEngine(t, agent, selfUpdateTemplates(cd))

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "truffels", CurrentVersion: "v0.1.0", LatestVersion: "v0.2.0", HasUpdate: true,
	})

	err := eng.ApplyUpdate("truffels")
	assertFailedWithAlert(t, st, "truffels", err, "git checkout failed")

	// Nothing was rewritten yet, so the file must still sit at the old version.
	assertComposeTags(t, composePath, "v0.1.0")
}

func TestApplySelfUpdate_ComposeRewriteFails(t *testing.T) {
	cd := t.TempDir()
	agent := newSelfUpdateMockAgent(mockAgentOpts{
		composeDirs:     map[string]string{"truffels": cd},
		rewriteFailFrom: 1,
	})
	defer agent.Close()

	composePath := writeSelfUpdateCompose(t, cd, "v0.1.0")
	eng, st := newTestEngine(t, agent, selfUpdateTemplates(cd))

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "truffels", CurrentVersion: "v0.1.0", LatestVersion: "v0.2.0", HasUpdate: true,
	})

	err := eng.ApplyUpdate("truffels")
	assertFailedWithAlert(t, st, "truffels", err, "compose rewrite failed")

	// The rewrite is fatal before anything is built: the file never moved.
	assertComposeTags(t, composePath, "v0.1.0")
}

func TestApplySelfUpdate_DetachedRestartFails(t *testing.T) {
	cd := t.TempDir()
	agent := newSelfUpdateMockAgent(mockAgentOpts{
		composeDirs:  map[string]string{"truffels": cd},
		labelsByRef:  selfUpdateLabels("v0.2.0"),
		detachedFail: true,
	})
	defer agent.Close()

	writeSelfUpdateCompose(t, cd, "v0.1.0")
	eng, st := newTestEngine(t, agent, selfUpdateTemplates(cd))

	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "truffels", CurrentVersion: "v0.1.0", LatestVersion: "v0.2.0", HasUpdate: true,
	})

	err := eng.ApplyUpdate("truffels")
	assertFailedWithAlert(t, st, "truffels", err, "detached restart failed")
}

// --- RunPreflight ---

func TestRunPreflight_DependencyNotRegistered(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	tmpl := mempoolTemplate(t, t.TempDir())
	tmpl.Dependencies = []string{"ghost"}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "mempool", CurrentVersion: "v3.2.0", LatestVersion: "v3.2.1", HasUpdate: true,
	})

	result, err := eng.RunPreflight("mempool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CanProceed {
		t.Error("expected CanProceed false when a dependency is not in the registry")
	}
	assertCheck(t, result, "dependency_ghost", "fail", "dependency not found in registry", true)
}

func TestRunPreflight_DependencyHealthy(t *testing.T) {
	agent := newMockAgent(mockAgentOpts{})
	defer agent.Close()

	dep := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ComposeDir:     t.TempDir(),
		ContainerNames: []string{"truffels-bitcoind"},
	}
	tmpl := mempoolTemplate(t, t.TempDir())
	tmpl.Dependencies = []string{"bitcoind"}

	eng, st := newTestEngine(t, agent, []model.ServiceTemplate{dep, tmpl})
	_ = st.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID: "mempool", CurrentVersion: "v3.2.0", LatestVersion: "v3.2.1", HasUpdate: true,
	})

	result, err := eng.RunPreflight("mempool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCheck(t, result, "dependency_bitcoind", "pass", "bitcoind is healthy", true)
}

// The disk check reads a fixed path that does not exist in a test container, so
// both arms are reached through the statfs seam rather than the real filesystem.
func TestRunPreflight_DiskSpace(t *testing.T) {
	cases := []struct {
		name       string
		bavail     uint64
		wantStatus string
		wantMsg    string
		wantCan    bool
	}{
		{"enough", 1 << 20, "pass", "4.0 GB available", true},
		{"too little", 1 << 17, "fail", "insufficient disk space: 0.5 GB available (need 2 GB)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := statfs
			t.Cleanup(func() { statfs = orig })
			statfs = func(_ string, buf *syscall.Statfs_t) error {
				*buf = syscall.Statfs_t{Bsize: 4096, Bavail: tc.bavail}
				return nil
			}

			agent := newMockAgent(mockAgentOpts{})
			defer agent.Close()

			tmpl := mempoolTemplate(t, t.TempDir())
			eng, st := newTestEngine(t, agent, []model.ServiceTemplate{tmpl})
			_ = st.UpsertUpdateCheck(&model.UpdateCheck{
				ServiceID: "mempool", CurrentVersion: "v3.2.0", LatestVersion: "v3.2.1", HasUpdate: true,
			})

			result, err := eng.RunPreflight("mempool")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertCheck(t, result, "disk_space", tc.wantStatus, tc.wantMsg, true)
			if result.CanProceed != tc.wantCan {
				t.Errorf("expected CanProceed %v, got %v", tc.wantCan, result.CanProceed)
			}
		})
	}
}

// assertCheck asserts that exactly one check by that name exists and carries the
// expected status, message and blocking flag.
func assertCheck(t *testing.T, r *model.PreflightResult, name, status, msg string, blocking bool) {
	t.Helper()
	var found []model.PreflightCheck
	for _, c := range r.Checks {
		if c.Name == name {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one %q check, got %d (%+v)", name, len(found), r.Checks)
	}
	c := found[0]
	if c.Status != status {
		t.Errorf("%s: expected status %q, got %q", name, status, c.Status)
	}
	if c.Message != msg {
		t.Errorf("%s: expected message %q, got %q", name, msg, c.Message)
	}
	if c.Blocking != blocking {
		t.Errorf("%s: expected blocking %v, got %v", name, blocking, c.Blocking)
	}
}
