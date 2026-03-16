package alerts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestCheckDisk_Critical(t *testing.T) {
	s := newTestStore(t)
	e := &Engine{store: s, lastRestartCounts: make(map[string]int)}

	e.checkDisk(model.DiskUsage{
		Path: "/srv", UsedPercent: 96.0, AvailGB: 10,
	})

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != model.SeverityCritical {
		t.Fatalf("expected critical, got %q", alerts[0].Severity)
	}
	if alerts[0].Type != "disk_full" {
		t.Fatalf("expected disk_full, got %q", alerts[0].Type)
	}
}

func TestCheckDisk_Warning(t *testing.T) {
	s := newTestStore(t)
	e := &Engine{store: s, lastRestartCounts: make(map[string]int)}

	e.checkDisk(model.DiskUsage{
		Path: "/srv", UsedPercent: 91.0, AvailGB: 50,
	})

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != model.SeverityWarning {
		t.Fatalf("expected warning, got %q", alerts[0].Severity)
	}
}

func TestCheckDisk_Normal(t *testing.T) {
	s := newTestStore(t)
	e := &Engine{store: s, lastRestartCounts: make(map[string]int)}

	// Create alert first, then resolve
	e.checkDisk(model.DiskUsage{Path: "/srv", UsedPercent: 96.0, AvailGB: 10})
	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert before resolve, got %d", len(alerts))
	}

	// Now disk is normal
	e.checkDisk(model.DiskUsage{Path: "/srv", UsedPercent: 85.0, AvailGB: 200})
	alerts, _ = s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 active alerts after resolve, got %d", len(alerts))
	}
}

func TestCheckTemp_Critical(t *testing.T) {
	s := newTestStore(t)
	e := &Engine{store: s, lastRestartCounts: make(map[string]int)}

	e.checkTemp(82.5)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1, got %d", len(alerts))
	}
	if alerts[0].Severity != model.SeverityCritical {
		t.Fatalf("expected critical, got %q", alerts[0].Severity)
	}
}

func TestCheckTemp_Warning(t *testing.T) {
	s := newTestStore(t)
	e := &Engine{store: s, lastRestartCounts: make(map[string]int)}

	e.checkTemp(76.0)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1, got %d", len(alerts))
	}
	if alerts[0].Severity != model.SeverityWarning {
		t.Fatalf("expected warning, got %q", alerts[0].Severity)
	}
}

func TestCheckTemp_Normal(t *testing.T) {
	s := newTestStore(t)
	e := &Engine{store: s, lastRestartCounts: make(map[string]int)}

	e.checkTemp(82.0)
	e.checkTemp(70.0)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 after cooling, got %d", len(alerts))
	}
}

func TestCheckTemp_Boundaries(t *testing.T) {
	tests := []struct {
		temp     float64
		severity model.AlertSeverity
		active   bool
	}{
		{74.9, "", false},
		{75.0, model.SeverityWarning, true},
		{79.9, model.SeverityWarning, true},
		{80.0, model.SeverityCritical, true},
		{90.0, model.SeverityCritical, true},
	}

	for _, tt := range tests {
		s := newTestStore(t)
		e := &Engine{store: s, lastRestartCounts: make(map[string]int)}
		e.checkTemp(tt.temp)

		alerts, _ := s.GetActiveAlerts()
		if tt.active && len(alerts) == 0 {
			t.Fatalf("temp=%.1f: expected alert", tt.temp)
		}
		if !tt.active && len(alerts) != 0 {
			t.Fatalf("temp=%.1f: expected no alert", tt.temp)
		}
		if tt.active && alerts[0].Severity != tt.severity {
			t.Fatalf("temp=%.1f: expected %q, got %q", tt.temp, tt.severity, alerts[0].Severity)
		}
	}
}

func TestSprintf(t *testing.T) {
	got := sprintf("disk %s is %.1f%% full", "/srv", 95.5)
	expected := "disk /srv is 95.5% full"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestEngine_StartStop(t *testing.T) {
	s := newTestStore(t)
	e := NewEngine(s, nil, nil, nil)
	e.Start()
	e.Stop()
	// Should not panic or hang
}

// --- Restart loop detection ---

func newTestEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	s := newTestStore(t)
	e := &Engine{
		store:              s,
		lastRestartCounts:  make(map[string]int),
		restartHistory:     make(map[string][]time.Time),
		autoStopped:        make(map[string]bool),
		prevStates:         make(map[string]model.ContainerState),
		prevContainerStats: make(map[string]docker.ContainerResourceStats),
	}
	return e, s
}

func TestRestartLoop_BelowThreshold(t *testing.T) {
	e, s := newTestEngine(t)

	// Simulate 3 restarts within window (default threshold=5)
	now := time.Now()
	e.restartHistory["test-container"] = []time.Time{
		now.Add(-3 * time.Minute),
		now.Add(-2 * time.Minute),
		now.Add(-1 * time.Minute),
	}

	// Manually run the windowed counting logic
	e.evalRestartLoop("test-svc", "test-container", 5, 10, 0)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts for 3 restarts (threshold 5), got %d", len(alerts))
	}
}

func TestRestartLoop_AtThreshold(t *testing.T) {
	e, s := newTestEngine(t)

	now := time.Now()
	e.restartHistory["test-container"] = []time.Time{
		now.Add(-4 * time.Minute),
		now.Add(-3 * time.Minute),
		now.Add(-2 * time.Minute),
		now.Add(-1 * time.Minute),
		now,
	}

	e.evalRestartLoop("test-svc", "test-container", 5, 10, 0)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert at threshold, got %d", len(alerts))
	}
	if alerts[0].Type != "restart_loop" {
		t.Fatalf("expected restart_loop, got %q", alerts[0].Type)
	}
	if alerts[0].Severity != model.SeverityCritical {
		t.Fatalf("expected critical, got %q", alerts[0].Severity)
	}
}

func TestRestartLoop_OldRestartsExpire(t *testing.T) {
	e, s := newTestEngine(t)

	// All restarts older than window — should be pruned
	old := time.Now().Add(-20 * time.Minute)
	e.restartHistory["test-container"] = []time.Time{
		old.Add(-4 * time.Minute),
		old.Add(-3 * time.Minute),
		old.Add(-2 * time.Minute),
		old.Add(-1 * time.Minute),
		old,
	}

	e.evalRestartLoop("test-svc", "test-container", 5, 10, 0)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts after expiry, got %d", len(alerts))
	}
	if len(e.restartHistory["test-container"]) != 0 {
		t.Fatalf("expected history pruned, got %d", len(e.restartHistory["test-container"]))
	}
}

func TestRestartLoop_ResolvesWhenStable(t *testing.T) {
	e, s := newTestEngine(t)

	// First trigger the alert
	now := time.Now()
	e.restartHistory["test-container"] = []time.Time{
		now.Add(-4 * time.Minute), now.Add(-3 * time.Minute),
		now.Add(-2 * time.Minute), now.Add(-1 * time.Minute), now,
	}
	e.evalRestartLoop("test-svc", "test-container", 5, 10, 0)
	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	// Now clear history (all expired)
	e.restartHistory["test-container"] = nil
	e.evalRestartLoop("test-svc", "test-container", 5, 10, 0)

	alerts, _ = s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts after stabilization, got %d", len(alerts))
	}
}

func TestRestartLoop_CustomThresholds(t *testing.T) {
	e, s := newTestEngine(t)

	// Set custom threshold via store: alert after 3 restarts
	_ = s.SetSetting("restart_loop_count", "3")

	now := time.Now()
	e.restartHistory["test-container"] = []time.Time{
		now.Add(-2 * time.Minute),
		now.Add(-1 * time.Minute),
		now,
	}

	// Use custom threshold
	e.evalRestartLoop("test-svc", "test-container", 3, 10, 0)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert at custom threshold 3, got %d", len(alerts))
	}
}

func TestRestartLoop_CountIncrement(t *testing.T) {
	e, _ := newTestEngine(t)

	// Simulate restart count going from 5 to 8 (3 new restarts)
	e.lastRestartCounts["test-container"] = 5
	e.recordRestartIncrements("test-container", 8)

	if len(e.restartHistory["test-container"]) != 3 {
		t.Fatalf("expected 3 restarts recorded, got %d", len(e.restartHistory["test-container"]))
	}
}

func TestRestartLoop_CountNoIncrement(t *testing.T) {
	e, _ := newTestEngine(t)

	e.lastRestartCounts["test-container"] = 5
	e.recordRestartIncrements("test-container", 5) // same count

	if len(e.restartHistory["test-container"]) != 0 {
		t.Fatalf("expected 0 restarts recorded, got %d", len(e.restartHistory["test-container"]))
	}
}

func TestRestartLoop_CountReset(t *testing.T) {
	e, _ := newTestEngine(t)

	// Container restarted and count reset (new container)
	e.lastRestartCounts["test-container"] = 10
	e.recordRestartIncrements("test-container", 2) // lower = container recreated

	if len(e.restartHistory["test-container"]) != 0 {
		t.Fatalf("expected 0 restarts on counter reset, got %d", len(e.restartHistory["test-container"]))
	}
}

func TestRestartLoop_AutoStopClearsOnStable(t *testing.T) {
	e, _ := newTestEngine(t)
	e.autoStopped["test-svc"] = true

	// Empty history = stable
	e.restartHistory["test-container"] = nil
	e.evalRestartLoop("test-svc", "test-container", 5, 10, 0)

	if e.autoStopped["test-svc"] {
		t.Fatal("expected autoStopped cleared after stabilization")
	}
}

func TestClampDelta(t *testing.T) {
	tests := []struct {
		cur, prev, want int64
	}{
		{100, 50, 50},
		{50, 100, 0}, // counter reset
		{0, 0, 0},
		{1000000, 999999, 1},
	}
	for _, tt := range tests {
		got := clampDelta(tt.cur, tt.prev)
		if got != tt.want {
			t.Fatalf("clampDelta(%d, %d) = %d, want %d", tt.cur, tt.prev, got, tt.want)
		}
	}
}

func TestCheckTemp_CustomThresholds(t *testing.T) {
	s := newTestStore(t)
	e := &Engine{store: s, lastRestartCounts: make(map[string]int)}

	// Set custom thresholds: warning=60, critical=70
	_ = s.SetSetting("temp_warning", "60")
	_ = s.SetSetting("temp_critical", "70")

	// 65C should be warning with custom thresholds (but not with defaults)
	e.checkTemp(65.0)
	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert at 65C with custom warning=60, got %d", len(alerts))
	}
	if alerts[0].Severity != model.SeverityWarning {
		t.Fatalf("expected warning, got %q", alerts[0].Severity)
	}
}

func TestCheckTemp_CustomCritical(t *testing.T) {
	s := newTestStore(t)
	e := &Engine{store: s, lastRestartCounts: make(map[string]int)}

	_ = s.SetSetting("temp_warning", "60")
	_ = s.SetSetting("temp_critical", "70")

	e.checkTemp(72.0)
	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert at 72C with custom critical=70, got %d", len(alerts))
	}
	if alerts[0].Severity != model.SeverityCritical {
		t.Fatalf("expected critical, got %q", alerts[0].Severity)
	}
}

// --- Disabled service alert suppression ---

// setupMockAgent creates a mock agent HTTP server that returns the given container states
// and sets the docker package's global agentClient. Returns cleanup function.
func setupMockAgent(t *testing.T, states map[string]model.ContainerState) {
	t.Helper()
	old := docker.SetAgentInspector(nil) // save current
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Containers []string `json:"containers"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		var result []model.ContainerState
		for _, name := range req.Containers {
			if cs, ok := states[name]; ok {
				result = append(result, cs)
			} else {
				result = append(result, model.ContainerState{Name: name, Status: "not_found", Health: "unknown"})
			}
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(func() {
		srv.Close()
		docker.SetAgentInspector(old)
	})
	docker.NewAgentInspector(srv.URL)
}

func newTestEngineWithRegistry(t *testing.T, tmpls []model.ServiceTemplate) (*Engine, *store.Store) {
	t.Helper()
	s := newTestStore(t)
	reg := service.NewTestRegistry(tmpls)
	e := &Engine{
		store:              s,
		registry:           reg,
		lastRestartCounts:  make(map[string]int),
		restartHistory:     make(map[string][]time.Time),
		autoStopped:        make(map[string]bool),
		prevStates:         make(map[string]model.ContainerState),
		prevContainerStats: make(map[string]docker.ContainerResourceStats),
	}
	return e, s
}

func TestCheckService_DisabledExited_NoAlert(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-electrs": {Name: "truffels-electrs", Status: "exited", Health: ""},
	})

	tmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ContainerNames: []string{"truffels-electrs"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{tmpl})

	// Disable the service
	_ = s.EnsureService("electrs")
	_ = s.SetServiceEnabled("electrs", false)

	e.checkService(tmpl)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts for disabled+exited service, got %d: %v", len(alerts), alerts)
	}
}

func TestCheckService_EnabledExited_Alerts(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-electrs": {Name: "truffels-electrs", Status: "exited", Health: ""},
	})

	tmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ContainerNames: []string{"truffels-electrs"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{tmpl})

	// Service is enabled by default
	e.checkService(tmpl)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert for enabled+exited service, got %d", len(alerts))
	}
	if alerts[0].Severity != model.SeverityWarning {
		t.Fatalf("expected warning, got %q", alerts[0].Severity)
	}
}

func TestCheckService_DisabledUnhealthy_StillAlerts(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-electrs": {Name: "truffels-electrs", Status: "running", Health: "unhealthy"},
	})

	tmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ContainerNames: []string{"truffels-electrs"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{tmpl})

	_ = s.EnsureService("electrs")
	_ = s.SetServiceEnabled("electrs", false)

	e.checkService(tmpl)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert for disabled but unhealthy (running) service, got %d", len(alerts))
	}
	if alerts[0].Severity != model.SeverityCritical {
		t.Fatalf("expected critical, got %q", alerts[0].Severity)
	}
}

func TestCheckService_DisabledExited_ResolvesExistingAlert(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-electrs": {Name: "truffels-electrs", Status: "exited", Health: ""},
	})

	tmpl := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ContainerNames: []string{"truffels-electrs"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{tmpl})

	// Create an existing alert (as if service was enabled before)
	e.upsert("service_unhealthy", "electrs", model.SeverityWarning, "Container truffels-electrs is exited")
	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("setup: expected 1 alert, got %d", len(alerts))
	}

	// Now disable and re-check — should resolve
	_ = s.EnsureService("electrs")
	_ = s.SetServiceEnabled("electrs", false)
	e.checkService(tmpl)

	alerts, _ = s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected alert resolved after disabling service, got %d", len(alerts))
	}
}

func TestCheckService_DisabledNotFound_NoAlert(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-ckpool": {Name: "truffels-ckpool", Status: "not_found", Health: ""},
	})

	tmpl := model.ServiceTemplate{
		ID:             "ckpool",
		DisplayName:    "ckpool",
		ContainerNames: []string{"truffels-ckpool"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{tmpl})

	_ = s.EnsureService("ckpool")
	_ = s.SetServiceEnabled("ckpool", false)

	e.checkService(tmpl)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts for disabled+not_found service, got %d", len(alerts))
	}
}

// --- Dependency health: disabled services ---

func TestCheckDependencyHealth_DisabledService_NoAlert(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-bitcoind": {Name: "truffels-bitcoind", Status: "exited", Health: ""},
	})

	bitcoind := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ContainerNames: []string{"truffels-bitcoind"},
	}
	electrs := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ContainerNames: []string{"truffels-electrs"},
		Dependencies:   []string{"bitcoind"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{bitcoind, electrs})

	// Disable electrs — it shouldn't get upstream_unhealthy alerts
	_ = s.EnsureService("electrs")
	_ = s.SetServiceEnabled("electrs", false)

	e.checkDependencyHealth()

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts for disabled dependent, got %d: %v", len(alerts), alerts)
	}
}

func TestCheckDependencyHealth_EnabledService_Alerts(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-bitcoind": {Name: "truffels-bitcoind", Status: "exited", Health: ""},
	})

	bitcoind := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ContainerNames: []string{"truffels-bitcoind"},
	}
	electrs := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ContainerNames: []string{"truffels-electrs"},
		Dependencies:   []string{"bitcoind"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{bitcoind, electrs})

	// electrs is enabled by default
	e.checkDependencyHealth()

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 upstream_unhealthy alert, got %d", len(alerts))
	}
	if alerts[0].Type != "upstream_unhealthy" {
		t.Fatalf("expected upstream_unhealthy, got %q", alerts[0].Type)
	}
}

func TestCheckDependencyHealth_DisabledService_ResolvesExisting(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-bitcoind": {Name: "truffels-bitcoind", Status: "exited", Health: ""},
	})

	bitcoind := model.ServiceTemplate{
		ID:             "bitcoind",
		DisplayName:    "Bitcoin Core",
		ContainerNames: []string{"truffels-bitcoind"},
	}
	electrs := model.ServiceTemplate{
		ID:             "electrs",
		DisplayName:    "electrs",
		ContainerNames: []string{"truffels-electrs"},
		Dependencies:   []string{"bitcoind"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{bitcoind, electrs})

	// Create an existing upstream_unhealthy alert
	e.upsert("upstream_unhealthy", "electrs", model.SeverityWarning, "Upstream bitcoind is unhealthy")
	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("setup: expected 1 alert, got %d", len(alerts))
	}

	// Disable electrs and re-check — should resolve
	_ = s.EnsureService("electrs")
	_ = s.SetServiceEnabled("electrs", false)
	e.checkDependencyHealth()

	alerts, _ = s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected alert resolved after disabling, got %d", len(alerts))
	}
}

// --- Read-only service alert suppression ---

func TestCheckService_ReadOnlyExited_AllDependentsDisabled_NoAlert(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-ckstats-db": {Name: "truffels-ckstats-db", Status: "exited", Health: ""},
	})

	ckstatsDB := model.ServiceTemplate{
		ID:             "ckstats-db",
		DisplayName:    "ckstats DB",
		ContainerNames: []string{"truffels-ckstats-db"},
		ReadOnly:       true,
	}
	ckstats := model.ServiceTemplate{
		ID:             "ckstats",
		DisplayName:    "ckstats",
		ContainerNames: []string{"truffels-ckstats"},
		Dependencies:   []string{"ckstats-db"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{ckstatsDB, ckstats})

	// Disable ckstats (the only dependent of ckstats-db)
	_ = s.EnsureService("ckstats")
	_ = s.SetServiceEnabled("ckstats", false)

	e.checkService(ckstatsDB)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts for read-only service with all dependents disabled, got %d: %v", len(alerts), alerts)
	}
}

func TestCheckService_ReadOnlyExited_SomeDependentsEnabled_Alerts(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-ckstats-db": {Name: "truffels-ckstats-db", Status: "exited", Health: ""},
	})

	ckstatsDB := model.ServiceTemplate{
		ID:             "ckstats-db",
		DisplayName:    "ckstats DB",
		ContainerNames: []string{"truffels-ckstats-db"},
		ReadOnly:       true,
	}
	ckstats := model.ServiceTemplate{
		ID:             "ckstats",
		DisplayName:    "ckstats",
		ContainerNames: []string{"truffels-ckstats"},
		Dependencies:   []string{"ckstats-db"},
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{ckstatsDB, ckstats})

	// ckstats is enabled by default — DB being exited is a real problem
	e.checkService(ckstatsDB)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert for read-only service with enabled dependents, got %d", len(alerts))
	}
}

func TestCheckService_ReadOnlyRunning_NoAlertRegardless(t *testing.T) {
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-ckstats-db": {Name: "truffels-ckstats-db", Status: "running", Health: "healthy"},
	})

	ckstatsDB := model.ServiceTemplate{
		ID:             "ckstats-db",
		DisplayName:    "ckstats DB",
		ContainerNames: []string{"truffels-ckstats-db"},
		ReadOnly:       true,
	}
	e, s := newTestEngineWithRegistry(t, []model.ServiceTemplate{ckstatsDB})

	e.checkService(ckstatsDB)

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts for healthy read-only service, got %d", len(alerts))
	}
}
