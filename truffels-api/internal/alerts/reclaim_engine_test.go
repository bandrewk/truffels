package alerts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
)

// reclaimTestDirPath must match watchedDirs[0].path (the mempool cache entry)
// so evalWatchedDirs' single watched-dir loop exercises the reclaim path.
const reclaimTestDirPath = "/srv/truffels/data/mempool/cache"

// mockComposeAgent is a fake agent HTTP server serving the endpoints
// ComposeClient hits during a reclaim attempt: dir-size, stop, clear-dir,
// and up. Each endpoint has an independent call counter and an optional
// failure switch so tests can force stop/clear/up to fail individually.
type mockComposeAgent struct {
	mu sync.Mutex

	dirSizeBytes int64

	stopCalls, clearCalls, upCalls int
	failStop, failClear, failUp    bool
}

func newMockComposeAgent(t *testing.T, dirSizeBytes int64) (*httptest.Server, *mockComposeAgent) {
	t.Helper()
	m := &mockComposeAgent{dirSizeBytes: dirSizeBytes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()

		switch r.URL.Path {
		case "/v1/host/dir-size":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"size_bytes": m.dirSizeBytes, "fresh": true,
			})
		case "/v1/compose/stop":
			m.stopCalls++
			if m.failStop {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "stop failed"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/v1/fs/clear-dir":
			m.clearCalls++
			if m.failClear {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "clear-dir failed"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/v1/compose/up":
			m.upCalls++
			if m.failUp {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "up failed"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, m
}

func (m *mockComposeAgent) counts() (stop, clear, up int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopCalls, m.clearCalls, m.upCalls
}

// reclaimTestTemplate builds a mempool service template with a single
// DataDirs entry matching watchedDirs[0].path.
func reclaimTestTemplate(clearable, requiresStop bool) model.ServiceTemplate {
	return model.ServiceTemplate{
		ID:             "mempool",
		DisplayName:    "mempool",
		ContainerNames: []string{"truffels-mempool-backend"},
		DataDirs: []model.DataDir{
			{
				Path:         reclaimTestDirPath,
				Label:        "cache",
				Clearable:    clearable,
				RequiresStop: requiresStop,
			},
		},
	}
}

// newReclaimTestEngine wires an engine with a mock compose agent (size,
// stop/clear/up) and a mock inspect agent (container running state), and
// arms e.reclaimDone so the caller can wait for tryReclaim's goroutine
// instead of sleeping.
func newReclaimTestEngine(t *testing.T, dirSizeBytes int64, containerStatus string, clearable, requiresStop bool) (*Engine, *mockComposeAgent) {
	t.Helper()
	setupMockAgent(t, map[string]model.ContainerState{
		"truffels-mempool-backend": {Name: "truffels-mempool-backend", Status: containerStatus},
	})

	tmpl := reclaimTestTemplate(clearable, requiresStop)
	e, _ := newTestEngineWithRegistry(t, []model.ServiceTemplate{tmpl})

	srv, mock := newMockComposeAgent(t, dirSizeBytes)
	e.compose = docker.NewComposeClient(srv.URL)
	e.reclaimDone = make(chan struct{}, 1)

	return e, mock
}

// waitReclaim runs evalWatchedDirs and blocks until the spawned tryReclaim
// goroutine (if any) has completed, or the deadline is hit.
func waitReclaim(t *testing.T, e *Engine) {
	t.Helper()
	e.evalWatchedDirs()
	select {
	case <-e.reclaimDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tryReclaim to complete")
	}
}

// TestTryReclaim_FailedClearDoesNotLoop is the regression test for the
// Critical defect found in fix round 1: a failed reclaim attempt must still
// consume the cooldown, or the engine stops and restarts the service every
// tick forever. clear-dir fails, the rollback up succeeds (service stays
// running), and evalWatchedDirs is driven twice — Stop must be called
// exactly once.
func TestTryReclaim_FailedClearDoesNotLoop(t *testing.T) {
	e, mock := newReclaimTestEngine(t, 950*1024*1024, "running", true, true)
	mock.failClear = true

	waitReclaim(t, e)
	waitReclaim(t, e)

	stop, clear, up := mock.counts()
	if stop != 1 {
		t.Fatalf("expected exactly 1 stop call across two attempts, got %d (clear=%d up=%d)", stop, clear, up)
	}
}

// TestTryReclaim_HappyPath asserts the full stop -> clear -> up sequence,
// exactly one auto_reclaim audit row, and that any existing
// auto_reclaim_failed alert is resolved.
func TestTryReclaim_HappyPath(t *testing.T) {
	e, mock := newReclaimTestEngine(t, 950*1024*1024, "running", true, true)

	// Seed a pre-existing failure alert to confirm it gets resolved.
	e.upsert("auto_reclaim_failed", "mempool", model.SeverityCritical, "prior failure")

	waitReclaim(t, e)

	stop, clear, up := mock.counts()
	if stop != 1 || clear != 1 || up != 1 {
		t.Fatalf("expected stop=1 clear=1 up=1, got stop=%d clear=%d up=%d", stop, clear, up)
	}

	entries, err := e.store.GetAuditLog(50)
	if err != nil {
		t.Fatalf("get audit log: %v", err)
	}
	var reclaimRows int
	for _, ent := range entries {
		if ent.Action == "auto_reclaim" && ent.Target == "mempool" {
			reclaimRows++
		}
	}
	if reclaimRows != 1 {
		t.Fatalf("expected exactly 1 auto_reclaim audit row, got %d", reclaimRows)
	}

	alerts, _ := e.store.GetActiveAlerts()
	for _, a := range alerts {
		if a.Type == "auto_reclaim_failed" {
			t.Fatalf("expected auto_reclaim_failed alert to be resolved, still active: %+v", a)
		}
	}
}

// TestTryReclaim_RespectsMinInterval pre-seeds an auto_reclaim audit row one
// hour old (well inside the 24h default cooldown) and asserts no agent
// mutation happens.
func TestTryReclaim_RespectsMinInterval(t *testing.T) {
	e, mock := newReclaimTestEngine(t, 950*1024*1024, "running", true, true)

	_ = e.store.LogAudit("auto_reclaim", "mempool", "prior attempt", "")
	// LogAudit uses the DB's own timestamp (now), which is inside the 24h
	// window by construction — no need to backdate for the interval guard
	// to trigger.

	waitReclaim(t, e)

	stop, clear, up := mock.counts()
	if stop != 0 || clear != 0 || up != 0 {
		t.Fatalf("expected zero agent mutations, got stop=%d clear=%d up=%d", stop, clear, up)
	}
}

// TestTryReclaim_RefusesNonClearableDir points the watched dir at a
// DataDirs entry that is not Clearable and asserts FsClearDir (and Stop) are
// never called.
func TestTryReclaim_RefusesNonClearableDir(t *testing.T) {
	e, mock := newReclaimTestEngine(t, 950*1024*1024, "running", false, true)

	waitReclaim(t, e)

	stop, clear, up := mock.counts()
	if stop != 0 || clear != 0 || up != 0 {
		t.Fatalf("expected zero agent mutations for non-clearable dir, got stop=%d clear=%d up=%d", stop, clear, up)
	}
}

// TestTryReclaim_ClearFailsRollsBackUp asserts Up is called even when
// clear-dir returns an error, and that the failure alert is raised.
func TestTryReclaim_ClearFailsRollsBackUp(t *testing.T) {
	e, mock := newReclaimTestEngine(t, 950*1024*1024, "running", true, true)
	mock.failClear = true

	waitReclaim(t, e)

	stop, clear, up := mock.counts()
	if stop != 1 || clear != 1 || up != 1 {
		t.Fatalf("expected stop=1 clear=1 up=1 (rollback), got stop=%d clear=%d up=%d", stop, clear, up)
	}

	alerts, _ := e.store.GetActiveAlerts()
	var found bool
	for _, a := range alerts {
		if a.Type == "auto_reclaim_failed" && a.ServiceID == "mempool" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected auto_reclaim_failed alert to be raised, active alerts: %+v", alerts)
	}
}

// TestTryReclaim_SkipsWhenServiceStopped asserts zero agent mutations when
// the container is not running.
func TestTryReclaim_SkipsWhenServiceStopped(t *testing.T) {
	e, mock := newReclaimTestEngine(t, 950*1024*1024, "exited", true, true)

	waitReclaim(t, e)

	stop, clear, up := mock.counts()
	if stop != 0 || clear != 0 || up != 0 {
		t.Fatalf("expected zero agent mutations when service is stopped, got stop=%d clear=%d up=%d", stop, clear, up)
	}
}
