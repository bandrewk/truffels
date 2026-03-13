package store

import (
	"os"
	"path/filepath"
	"testing"

	"truffels-api/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNew_CreatesDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("database file was not created")
	}
}

func TestNew_InvalidPath(t *testing.T) {
	_, err := New("/nonexistent/dir/test.db")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

// --- Settings ---

func TestSettings_GetEmpty(t *testing.T) {
	s := newTestStore(t)
	val, err := s.GetSetting("nonexistent")
	if err != nil {
		t.Fatalf("get setting: %v", err)
	}
	if val != "" {
		t.Fatalf("expected empty string, got %q", val)
	}
}

func TestSettings_SetAndGet(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetSetting("key1", "value1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	val, err := s.GetSetting("key1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "value1" {
		t.Fatalf("expected value1, got %q", val)
	}
}

func TestSettings_Upsert(t *testing.T) {
	s := newTestStore(t)
	_ = s.SetSetting("key1", "v1")
	_ = s.SetSetting("key1", "v2")

	val, _ := s.GetSetting("key1")
	if val != "v2" {
		t.Fatalf("expected v2 after upsert, got %q", val)
	}
}

// --- Audit Log ---

func TestAuditLog_InsertAndRetrieve(t *testing.T) {
	s := newTestStore(t)
	if err := s.LogAudit("test_action", "target1", "detail1", "127.0.0.1"); err != nil {
		t.Fatalf("log audit: %v", err)
	}
	entries, err := s.GetAuditLog(10)
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Action != "test_action" {
		t.Fatalf("expected test_action, got %q", entries[0].Action)
	}
	if entries[0].Target != "target1" {
		t.Fatalf("expected target1, got %q", entries[0].Target)
	}
	if entries[0].IP != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1, got %q", entries[0].IP)
	}
}

func TestAuditLog_Ordering(t *testing.T) {
	s := newTestStore(t)
	_ = s.LogAudit("first", "", "", "")
	_ = s.LogAudit("second", "", "", "")
	_ = s.LogAudit("third", "", "", "")

	entries, _ := s.GetAuditLog(10)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// Most recent first
	if entries[0].Action != "third" {
		t.Fatalf("expected third first, got %q", entries[0].Action)
	}
}

func TestAuditLog_Limit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 10; i++ {
		_ = s.LogAudit("action", "", "", "")
	}

	entries, _ := s.GetAuditLog(3)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries with limit, got %d", len(entries))
	}
}

// --- Service Enabled ---

func TestService_DefaultEnabled(t *testing.T) {
	s := newTestStore(t)
	enabled, err := s.IsServiceEnabled("nonexistent")
	if err != nil {
		t.Fatalf("is enabled: %v", err)
	}
	if !enabled {
		t.Fatal("expected default true for unknown service")
	}
}

func TestService_EnsureAndDisable(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureService("bitcoind"); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	// Default is enabled (1)
	enabled, _ := s.IsServiceEnabled("bitcoind")
	if !enabled {
		t.Fatal("expected enabled after ensure")
	}

	// Disable
	if err := s.SetServiceEnabled("bitcoind", false); err != nil {
		t.Fatalf("set disabled: %v", err)
	}
	enabled, _ = s.IsServiceEnabled("bitcoind")
	if enabled {
		t.Fatal("expected disabled")
	}

	// Re-enable
	_ = s.SetServiceEnabled("bitcoind", true)
	enabled, _ = s.IsServiceEnabled("bitcoind")
	if !enabled {
		t.Fatal("expected re-enabled")
	}
}

func TestService_EnsureDuplicate(t *testing.T) {
	s := newTestStore(t)
	_ = s.EnsureService("bitcoind")
	err := s.EnsureService("bitcoind")
	if err != nil {
		t.Fatalf("duplicate ensure should not error: %v", err)
	}
}

// --- Config Revisions ---

func TestConfigRevisions_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	rev := &model.ConfigRevision{
		ServiceID:        "bitcoind",
		Actor:            "admin",
		Diff:             "config updated",
		ConfigSnapshot:   "server=1\ntxindex=1\n",
		ValidationResult: "ok",
	}
	if err := s.CreateConfigRevision(rev); err != nil {
		t.Fatalf("create revision: %v", err)
	}

	revs, err := s.GetConfigRevisions("bitcoind", 10)
	if err != nil {
		t.Fatalf("get revisions: %v", err)
	}
	if len(revs) != 1 {
		t.Fatalf("expected 1 revision, got %d", len(revs))
	}
	if revs[0].Actor != "admin" {
		t.Fatalf("expected admin, got %q", revs[0].Actor)
	}
	if revs[0].ConfigSnapshot != "server=1\ntxindex=1\n" {
		t.Fatalf("snapshot mismatch")
	}
}

func TestConfigRevisions_Empty(t *testing.T) {
	s := newTestStore(t)
	revs, err := s.GetConfigRevisions("nonexistent", 10)
	if err != nil {
		t.Fatalf("get revisions: %v", err)
	}
	if len(revs) != 0 {
		t.Fatalf("expected 0 revisions, got %d", len(revs))
	}
}

func TestConfigRevisions_Limit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 10; i++ {
		_ = s.CreateConfigRevision(&model.ConfigRevision{
			ServiceID: "test", Actor: "admin", Diff: "changed",
			ConfigSnapshot: "v", ValidationResult: "ok",
		})
	}

	revs, _ := s.GetConfigRevisions("test", 3)
	if len(revs) != 3 {
		t.Fatalf("expected 3, got %d", len(revs))
	}
}

// --- Alerts ---

func TestAlerts_UpsertNew(t *testing.T) {
	s := newTestStore(t)
	err := s.UpsertAlert(&model.Alert{
		Type:      "disk_full",
		Severity:  model.SeverityWarning,
		ServiceID: "",
		Message:   "Disk 90% full",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	alerts, err := s.GetActiveAlerts()
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Message != "Disk 90% full" {
		t.Fatalf("message mismatch: %q", alerts[0].Message)
	}
}

func TestAlerts_UpsertExisting(t *testing.T) {
	s := newTestStore(t)
	_ = s.UpsertAlert(&model.Alert{
		Type: "disk_full", Severity: model.SeverityWarning, Message: "90%",
	})
	// Upsert same type → should update, not create new
	_ = s.UpsertAlert(&model.Alert{
		Type: "disk_full", Severity: model.SeverityCritical, Message: "95%",
	})

	alerts, _ := s.GetActiveAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert after upsert, got %d", len(alerts))
	}
	if alerts[0].Severity != model.SeverityCritical {
		t.Fatalf("expected critical, got %q", alerts[0].Severity)
	}
	if alerts[0].Message != "95%" {
		t.Fatalf("expected updated message, got %q", alerts[0].Message)
	}
}

func TestAlerts_Resolve(t *testing.T) {
	s := newTestStore(t)
	_ = s.UpsertAlert(&model.Alert{
		Type: "high_temp", Severity: model.SeverityWarning, Message: "75C",
	})

	err := s.ResolveAlerts("high_temp", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	active, _ := s.GetActiveAlerts()
	if len(active) != 0 {
		t.Fatalf("expected 0 active after resolve, got %d", len(active))
	}

	// Should still appear in all alerts
	all, _ := s.GetAllAlerts(10)
	if len(all) != 1 {
		t.Fatalf("expected 1 in all alerts, got %d", len(all))
	}
	if !all[0].Resolved {
		t.Fatal("expected resolved=true")
	}
}

func TestAlerts_ResolveOnlyMatchingType(t *testing.T) {
	s := newTestStore(t)
	_ = s.UpsertAlert(&model.Alert{
		Type: "disk_full", Severity: model.SeverityWarning, Message: "disk",
	})
	_ = s.UpsertAlert(&model.Alert{
		Type: "high_temp", Severity: model.SeverityWarning, Message: "temp",
	})

	_ = s.ResolveAlerts("disk_full", "")

	active, _ := s.GetActiveAlerts()
	if len(active) != 1 {
		t.Fatalf("expected 1 active, got %d", len(active))
	}
	if active[0].Type != "high_temp" {
		t.Fatalf("expected high_temp to remain, got %q", active[0].Type)
	}
}

func TestAlerts_ServiceScoped(t *testing.T) {
	s := newTestStore(t)
	_ = s.UpsertAlert(&model.Alert{
		Type: "service_unhealthy", Severity: model.SeverityCritical,
		ServiceID: "bitcoind", Message: "unhealthy",
	})
	_ = s.UpsertAlert(&model.Alert{
		Type: "service_unhealthy", Severity: model.SeverityCritical,
		ServiceID: "electrs", Message: "unhealthy",
	})

	// Resolve only bitcoind
	_ = s.ResolveAlerts("service_unhealthy", "bitcoind")

	active, _ := s.GetActiveAlerts()
	if len(active) != 1 {
		t.Fatalf("expected 1 active, got %d", len(active))
	}
	if active[0].ServiceID != "electrs" {
		t.Fatalf("expected electrs to remain, got %q", active[0].ServiceID)
	}
}

func TestAlerts_GetAllLimit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 10; i++ {
		_ = s.UpsertAlert(&model.Alert{
			Type: "test", Severity: model.SeverityWarning, Message: "test",
			ServiceID: string(rune('a' + i)),
		})
	}

	all, _ := s.GetAllAlerts(3)
	if len(all) != 3 {
		t.Fatalf("expected 3, got %d", len(all))
	}
}
