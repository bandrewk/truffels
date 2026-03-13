package store

import (
	"testing"

	"truffels-api/internal/model"
)

// --- UpsertUpdateCheck ---

func TestUpsertUpdateCheck_InsertNew(t *testing.T) {
	s := newTestStore(t)
	err := s.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "29.0",
		LatestVersion:  "29.1",
		HasUpdate:      true,
		Error:          "",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	c, err := s.GetLatestUpdateCheck("bitcoind")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil check")
	}
	if c.ServiceID != "bitcoind" {
		t.Fatalf("expected bitcoind, got %q", c.ServiceID)
	}
	if c.CurrentVersion != "29.0" {
		t.Fatalf("expected 29.0, got %q", c.CurrentVersion)
	}
	if c.LatestVersion != "29.1" {
		t.Fatalf("expected 29.1, got %q", c.LatestVersion)
	}
	if !c.HasUpdate {
		t.Fatal("expected has_update=true")
	}
}

func TestUpsertUpdateCheck_ReplacesExisting(t *testing.T) {
	s := newTestStore(t)
	_ = s.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "electrs",
		CurrentVersion: "0.10.9",
		LatestVersion:  "0.10.10",
		HasUpdate:      true,
	})
	// Upsert again with new data
	_ = s.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "electrs",
		CurrentVersion: "0.10.10",
		LatestVersion:  "0.10.10",
		HasUpdate:      false,
	})

	c, _ := s.GetLatestUpdateCheck("electrs")
	if c == nil {
		t.Fatal("expected non-nil check")
	}
	if c.CurrentVersion != "0.10.10" {
		t.Fatalf("expected updated current version 0.10.10, got %q", c.CurrentVersion)
	}
	if c.HasUpdate {
		t.Fatal("expected has_update=false after upsert")
	}
}

func TestUpsertUpdateCheck_OnlyOneRowPerService(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		_ = s.UpsertUpdateCheck(&model.UpdateCheck{
			ServiceID:      "bitcoind",
			CurrentVersion: "29.0",
			LatestVersion:  "29.0",
		})
	}

	checks, err := s.GetAllUpdateChecks()
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("expected 1 row after repeated upserts, got %d", len(checks))
	}
}

// --- GetLatestUpdateCheck ---

func TestGetLatestUpdateCheck_Found(t *testing.T) {
	s := newTestStore(t)
	_ = s.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.0.1",
		HasUpdate:      true,
	})

	c, err := s.GetLatestUpdateCheck("ckpool")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil")
	}
	if c.ServiceID != "ckpool" {
		t.Fatalf("expected ckpool, got %q", c.ServiceID)
	}
	if c.CheckedAt.IsZero() {
		t.Fatal("expected non-zero checked_at")
	}
}

func TestGetLatestUpdateCheck_NotFound(t *testing.T) {
	s := newTestStore(t)
	c, err := s.GetLatestUpdateCheck("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c != nil {
		t.Fatalf("expected nil for missing service, got %+v", c)
	}
}

// --- GetAllUpdateChecks ---

func TestGetAllUpdateChecks_MultipleServices(t *testing.T) {
	s := newTestStore(t)
	services := []string{"bitcoind", "electrs", "mempool"}
	for _, svc := range services {
		_ = s.UpsertUpdateCheck(&model.UpdateCheck{
			ServiceID:      svc,
			CurrentVersion: "1.0",
			LatestVersion:  "1.1",
			HasUpdate:      true,
		})
	}

	checks, err := s.GetAllUpdateChecks()
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(checks))
	}
	// Should be ordered by service_id
	if checks[0].ServiceID != "bitcoind" {
		t.Fatalf("expected bitcoind first, got %q", checks[0].ServiceID)
	}
	if checks[1].ServiceID != "electrs" {
		t.Fatalf("expected electrs second, got %q", checks[1].ServiceID)
	}
	if checks[2].ServiceID != "mempool" {
		t.Fatalf("expected mempool third, got %q", checks[2].ServiceID)
	}
}

func TestGetAllUpdateChecks_Empty(t *testing.T) {
	s := newTestStore(t)
	checks, err := s.GetAllUpdateChecks()
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("expected 0 checks, got %d", len(checks))
	}
}

// --- CreateUpdateLog + GetUpdateLogs ---

func TestCreateUpdateLog_AndRetrieve(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateUpdateLog(&model.UpdateLog{
		ServiceID:   "bitcoind",
		FromVersion: "29.0",
		ToVersion:   "29.1",
		Status:      model.UpdatePending,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	logs, err := s.GetUpdateLogs("bitcoind", 10)
	if err != nil {
		t.Fatalf("get logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if logs[0].ServiceID != "bitcoind" {
		t.Fatalf("expected bitcoind, got %q", logs[0].ServiceID)
	}
	if logs[0].FromVersion != "29.0" {
		t.Fatalf("expected 29.0, got %q", logs[0].FromVersion)
	}
	if logs[0].ToVersion != "29.1" {
		t.Fatalf("expected 29.1, got %q", logs[0].ToVersion)
	}
	if logs[0].Status != model.UpdatePending {
		t.Fatalf("expected pending, got %q", logs[0].Status)
	}
	if logs[0].StartedAt.IsZero() {
		t.Fatal("expected non-zero started_at")
	}
	if logs[0].CompletedAt != nil {
		t.Fatal("expected nil completed_at for pending log")
	}
}

func TestGetUpdateLogs_FilterByService(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "bitcoind", FromVersion: "29.0", ToVersion: "29.1", Status: model.UpdatePending,
	})
	_, _ = s.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "electrs", FromVersion: "0.10.9", ToVersion: "0.10.10", Status: model.UpdatePending,
	})
	_, _ = s.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "bitcoind", FromVersion: "29.1", ToVersion: "29.2", Status: model.UpdatePending,
	})

	// Filter by bitcoind
	logs, _ := s.GetUpdateLogs("bitcoind", 10)
	if len(logs) != 2 {
		t.Fatalf("expected 2 bitcoind logs, got %d", len(logs))
	}
	for _, l := range logs {
		if l.ServiceID != "bitcoind" {
			t.Fatalf("expected bitcoind, got %q", l.ServiceID)
		}
	}

	// Filter by electrs
	logs, _ = s.GetUpdateLogs("electrs", 10)
	if len(logs) != 1 {
		t.Fatalf("expected 1 electrs log, got %d", len(logs))
	}

	// All services (empty filter)
	logs, _ = s.GetUpdateLogs("", 10)
	if len(logs) != 3 {
		t.Fatalf("expected 3 total logs, got %d", len(logs))
	}
}

func TestGetUpdateLogs_Limit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 10; i++ {
		_, _ = s.CreateUpdateLog(&model.UpdateLog{
			ServiceID: "bitcoind", FromVersion: "1.0", ToVersion: "1.1", Status: model.UpdatePending,
		})
	}

	logs, _ := s.GetUpdateLogs("bitcoind", 3)
	if len(logs) != 3 {
		t.Fatalf("expected 3 with limit, got %d", len(logs))
	}
}

// --- UpdateLogStatus ---

func TestUpdateLogStatus_Transitions(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "bitcoind", FromVersion: "29.0", ToVersion: "29.1", Status: model.UpdatePending,
	})

	// Transition to pulling
	err := s.UpdateLogStatus(id, model.UpdatePulling, "", "")
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	logs, _ := s.GetUpdateLogs("bitcoind", 1)
	if logs[0].Status != model.UpdatePulling {
		t.Fatalf("expected pulling, got %q", logs[0].Status)
	}

	// Transition to done
	err = s.UpdateLogStatus(id, model.UpdateDone, "", "")
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	logs, _ = s.GetUpdateLogs("bitcoind", 1)
	if logs[0].Status != model.UpdateDone {
		t.Fatalf("expected done, got %q", logs[0].Status)
	}
	if logs[0].CompletedAt == nil {
		t.Fatal("expected non-nil completed_at after done")
	}
}

func TestUpdateLogStatus_ErrorAndRollback(t *testing.T) {
	s := newTestStore(t)
	id, _ := s.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "electrs", FromVersion: "0.10.9", ToVersion: "0.10.10", Status: model.UpdatePending,
	})

	err := s.UpdateLogStatus(id, model.UpdateFailed, "pull timeout", "")
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	logs, _ := s.GetUpdateLogs("electrs", 1)
	if logs[0].Status != model.UpdateFailed {
		t.Fatalf("expected failed, got %q", logs[0].Status)
	}
	if logs[0].Error != "pull timeout" {
		t.Fatalf("expected error message, got %q", logs[0].Error)
	}

	// New entry with rollback
	id2, _ := s.CreateUpdateLog(&model.UpdateLog{
		ServiceID: "electrs", FromVersion: "0.10.9", ToVersion: "0.10.10", Status: model.UpdatePending,
	})
	err = s.UpdateLogStatus(id2, model.UpdateRolledBack, "healthcheck failed", "0.10.9")
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	logs, _ = s.GetUpdateLogs("electrs", 1)
	if logs[0].Status != model.UpdateRolledBack {
		t.Fatalf("expected rolled_back, got %q", logs[0].Status)
	}
	if logs[0].RollbackVersion != "0.10.9" {
		t.Fatalf("expected rollback version 0.10.9, got %q", logs[0].RollbackVersion)
	}
	if logs[0].CompletedAt == nil {
		t.Fatal("expected non-nil completed_at after rollback")
	}
}

// --- PendingUpdateCount ---

func TestPendingUpdateCount_CountsOnlyUpdatesWithoutError(t *testing.T) {
	s := newTestStore(t)

	// No checks at all
	count, err := s.PendingUpdateCount()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 with no checks, got %d", count)
	}

	// Service with update available
	_ = s.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "bitcoind",
		CurrentVersion: "29.0",
		LatestVersion:  "29.1",
		HasUpdate:      true,
		Error:          "",
	})
	count, _ = s.PendingUpdateCount()
	if count != 1 {
		t.Fatalf("expected 1 pending, got %d", count)
	}

	// Service with no update
	_ = s.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "electrs",
		CurrentVersion: "0.10.10",
		LatestVersion:  "0.10.10",
		HasUpdate:      false,
		Error:          "",
	})
	count, _ = s.PendingUpdateCount()
	if count != 1 {
		t.Fatalf("expected still 1 pending, got %d", count)
	}

	// Service with update but also an error (should not count)
	_ = s.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "mempool",
		CurrentVersion: "3.2.0",
		LatestVersion:  "3.3.0",
		HasUpdate:      true,
		Error:          "registry unreachable",
	})
	count, _ = s.PendingUpdateCount()
	if count != 1 {
		t.Fatalf("expected 1 pending (error check excluded), got %d", count)
	}

	// Another service with a clean update
	_ = s.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      "ckpool",
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.0.1",
		HasUpdate:      true,
		Error:          "",
	})
	count, _ = s.PendingUpdateCount()
	if count != 2 {
		t.Fatalf("expected 2 pending, got %d", count)
	}
}
