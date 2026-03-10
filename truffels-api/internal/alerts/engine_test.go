package alerts

import (
	"path/filepath"
	"testing"

	"truffels-api/internal/model"
	"truffels-api/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
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
	e := NewEngine(s, nil, nil)
	e.Start()
	e.Stop()
	// Should not panic or hang
}
