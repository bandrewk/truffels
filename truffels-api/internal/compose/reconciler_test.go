package compose

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
)

func TestReconciler_NoChange(t *testing.T) {
	// Render the expected content for ckpool with current tag
	expected, err := Render("ckpool", CkpoolParams{ImageTag: "truffels/ckpool:v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}

	// Start mock agent that returns the expected content (no change needed)
	srv := newMockAgent(t, map[string]string{
		"ckpool": expected,
	}, nil)
	defer srv.Close()

	reg := service.NewTestRegistry([]model.ServiceTemplate{
		{ID: "ckpool", ComposeDir: "/srv/truffels/compose/ckpool"},
	})

	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL), nil)
	if err := reconciler.Run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReconciler_Changed(t *testing.T) {
	// Return old content with 256M — reconciler should detect diff
	oldContent := `# Project Truffels — ckpool (Solo Mining Pool)
# Managed by truffels. Do not edit manually.

services:
  ckpool:
    image: truffels/ckpool:v1.0.0
    container_name: truffels-ckpool
    restart: unless-stopped
    deploy:
      resources:
        limits:
          memory: 256M

networks:
  bitcoin-backend:
    external: true
`

	var reconciled bool
	var upped bool
	srv := newMockAgentFull(t, map[string]string{
		"ckpool": oldContent,
	}, func(serviceID, content string) (bool, error) {
		reconciled = true
		return true, nil // agent says it wrote
	}, func(serviceID string) error {
		upped = true
		return nil
	})
	defer srv.Close()

	reg := service.NewTestRegistry([]model.ServiceTemplate{
		{ID: "ckpool", ComposeDir: "/srv/truffels/compose/ckpool"},
	})

	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL), nil)
	if err := reconciler.Run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reconciled {
		t.Error("expected reconcile to be called")
	}
	if !upped {
		t.Error("expected up to be called after change")
	}
}

func TestReconciler_ReadError(t *testing.T) {
	srv := newMockAgentError(t)
	defer srv.Close()

	reg := service.NewTestRegistry([]model.ServiceTemplate{
		{ID: "ckpool", ComposeDir: "/srv/truffels/compose/ckpool"},
	})

	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL), nil)
	err := reconciler.Run()
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Mock agent helpers ---

func newMockAgent(t *testing.T, contents map[string]string, reconcileResults map[string]bool) *httptest.Server {
	t.Helper()
	return newMockAgentFull(t, contents,
		func(serviceID, content string) (bool, error) {
			if reconcileResults != nil {
				return reconcileResults[serviceID], nil
			}
			return false, nil
		},
		func(serviceID string) error { return nil },
	)
}

func newMockAgentFull(t *testing.T, contents map[string]string,
	reconcileFn func(string, string) (bool, error),
	upFn func(string) error,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/v1/compose/read":
			var req struct {
				ServiceID string `json:"service_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			content, ok := contents[req.ServiceID]
			if !ok {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "ok",
				"content": content,
			})

		case "/v1/compose/reconcile":
			var req struct {
				ServiceID       string `json:"service_id"`
				ExpectedContent string `json:"expected_content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			changed, err := reconcileFn(req.ServiceID, req.ExpectedContent)
			if err != nil {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "ok",
				"changed": changed,
			})

		case "/v1/compose/up":
			var req struct {
				ServiceID string `json:"service_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if err := upFn(req.ServiceID); err != nil {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		case "/v1/fs/ensure-dir":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "ok",
				"created": false,
			})

		default:
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"error":"not found: %s"}`, r.URL.Path)
		}
	}))
}

// captureAlertStore records UpsertAlert + ResolveAlerts calls in memory for testing.
type captureAlertStore struct {
	alerts   []*model.Alert
	resolved []resolveCall
}

type resolveCall struct {
	alertType string
	serviceID string
}

func (c *captureAlertStore) UpsertAlert(a *model.Alert) error {
	c.alerts = append(c.alerts, a)
	return nil
}

func (c *captureAlertStore) ResolveAlerts(alertType, serviceID string) error {
	c.resolved = append(c.resolved, resolveCall{alertType, serviceID})
	return nil
}

func TestReconciler_FailedUpEmitsAlert(t *testing.T) {
	// Render expected (so reconcile reports "changed") but make `compose up`
	// fail — reconciler must raise a critical Alert via the alertStore.
	oldContent := `services:
  ckpool:
    image: truffels/ckpool:v1.0.0
    deploy:
      resources:
        limits:
          memory: 256M
`
	srv := newMockAgentFull(t, map[string]string{"ckpool": oldContent},
		func(serviceID, content string) (bool, error) { return true, nil },
		func(serviceID string) error { return fmt.Errorf("compose up exit 1") },
	)
	defer srv.Close()

	reg := service.NewTestRegistry([]model.ServiceTemplate{
		{ID: "ckpool", ComposeDir: "/srv/truffels/compose/ckpool"},
	})
	store := &captureAlertStore{}

	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL), store)
	_ = reconciler.Run() // expected to return error

	if len(store.alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(store.alerts))
	}
	got := store.alerts[0]
	if got.Type != "compose_reconcile_failed" {
		t.Errorf("type: %q", got.Type)
	}
	if got.Severity != model.SeverityCritical {
		t.Errorf("severity: %q", got.Severity)
	}
	if got.ServiceID != "ckpool" {
		t.Errorf("service: %q", got.ServiceID)
	}
}

func newMockAgentError(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "agent unavailable"})
	}))
}

// TestReconciler_TruffelsShortCircuitsLoop verifies that when truffels
// reconciliation triggers a detached restart, no subsequent service is
// reconciled in this cycle (the next API boot picks them up).
func TestReconciler_TruffelsShortCircuitsLoop(t *testing.T) {
	truffelsOld := `services:
  agent:
    image: truffels/agent:v0.3.1-dev.15
    container_name: truffels-agent
    volumes:
      - /home/truffel/Project-Truffels:/repo:rw
  api:
    image: truffels/api:v0.3.1-dev.15
    container_name: truffels-api
  web:
    image: truffels/web:v0.3.1-dev.15
    container_name: truffels-web
`
	proxyOld := `services:
  proxy:
    image: caddy:2.11.2-alpine
    container_name: truffels-proxy
`
	var proxyComposeReadCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/compose/read":
			var req struct {
				ServiceID string `json:"service_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.ServiceID == "proxy" {
				proxyComposeReadCalled = true
			}
			content := truffelsOld
			if req.ServiceID == "proxy" {
				content = proxyOld
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "content": content,
			})
		case "/v1/compose/reconcile":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "changed": true,
			})
		case "/v1/compose/up-detached":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	reg := service.NewTestRegistry([]model.ServiceTemplate{
		{ID: "truffels", ComposeDir: "/srv/truffels/compose/truffels"},
		{ID: "proxy", ComposeDir: "/srv/truffels/compose/proxy"},
	})
	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL), nil)
	_ = reconciler.Run()

	if proxyComposeReadCalled {
		t.Error("proxy must NOT be reconciled when truffels triggered a short-circuit")
	}
}

// TestReconciler_TruffelsTriggersDetachedRestart verifies that when the
// truffels compose has drifted, the reconciler calls ComposeUpDetached
// (so the API survives its own restart) rather than Up.
func TestReconciler_TruffelsTriggersDetachedRestart(t *testing.T) {
	dev14Compose := `services:
  agent:
    image: truffels/agent:v0.3.1-dev.14
    container_name: truffels-agent
    volumes:
      - /home/truffel/Project-Truffels:/repo:rw
  api:
    image: truffels/api:v0.3.1-dev.14
    container_name: truffels-api
  web:
    image: truffels/web:v0.3.1-dev.14
    container_name: truffels-web
`
	var detachedCalled bool
	var upCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/compose/read":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "content": dev14Compose,
			})
		case "/v1/compose/reconcile":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "changed": true,
			})
		case "/v1/compose/up-detached":
			var req struct {
				ServiceID string `json:"service_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.ServiceID == "truffels" {
				detachedCalled = true
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/v1/compose/up":
			upCalled = true
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	reg := service.NewTestRegistry([]model.ServiceTemplate{
		{ID: "truffels", ComposeDir: "/srv/truffels/compose/truffels"},
	})
	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL), nil)
	_ = reconciler.Run()

	if !detachedCalled {
		t.Error("expected ComposeUpDetached for truffels")
	}
	if upCalled {
		t.Error("ComposeUp must NOT be called for truffels (would kill the running API)")
	}
}

// TestReconciler_ProxyWritesBothComposeAndCaddyfile verifies the proxy
// reconciler also writes /srv/truffels/config/proxy/Caddyfile.
func TestReconciler_ProxyWritesBothComposeAndCaddyfile(t *testing.T) {
	oldProxy := `services:
  proxy:
    image: caddy:2.11.2-alpine
    container_name: truffels-proxy
`
	writes := make(map[string]string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/compose/read":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "content": oldProxy,
			})
		case "/v1/compose/reconcile":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "changed": true,
			})
		case "/v1/file/reconcile":
			var req struct {
				Path            string `json:"path"`
				ExpectedContent string `json:"expected_content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			writes[req.Path] = req.ExpectedContent
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "changed": true,
			})
		case "/v1/compose/up":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	reg := service.NewTestRegistry([]model.ServiceTemplate{
		{ID: "proxy", ComposeDir: "/srv/truffels/compose/proxy"},
	})
	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL), nil)
	_ = reconciler.Run()

	caddy, ok := writes["/srv/truffels/config/proxy/Caddyfile"]
	if !ok {
		t.Errorf("Caddyfile not written; got writes=%v", writes)
	}
	if !strings.Contains(caddy, "/proxy-health") {
		t.Errorf("Caddyfile missing /proxy-health route: %s", caddy)
	}
}

// TestReconciler_ResolvesAlertOnSuccess verifies a stale
// compose_reconcile_failed alert is resolved when the next reconciliation
// for that service completes successfully.
func TestReconciler_ResolvesAlertOnSuccess(t *testing.T) {
	ckpoolUnchanged := `services:
  ckpool:
    image: truffels/ckpool:v1.0.0
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/compose/read":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "content": ckpoolUnchanged,
			})
		case "/v1/compose/reconcile":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "changed": false,
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	reg := service.NewTestRegistry([]model.ServiceTemplate{
		{ID: "ckpool", ComposeDir: "/srv/truffels/compose/ckpool"},
	})
	store := &captureAlertStore{}

	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL), store)
	if err := reconciler.Run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, rc := range store.resolved {
		if rc.alertType == "compose_reconcile_failed" && rc.serviceID == "ckpool" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ResolveAlerts(compose_reconcile_failed, ckpool); got %v", store.resolved)
	}
}

// TestReconciler_EnsureDirFailsButComposeStillWritten — even if ensure-dir
// fails (e.g. agent permission issue), the new compose still gets written
// and a critical alert is emitted. Mempool will fail loud rather than
// silently skipping the whole reconciliation.
func TestReconciler_EnsureDirFailsButComposeStillWritten(t *testing.T) {
	mempoolOld := `services:
  mempool-backend:
    image: mempool/backend:v3.3.1
  mempool-frontend:
    image: mempool/frontend:v3.3.1
  mempool-db:
    image: mariadb:lts
`
	var composeWritten bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/compose/read":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "content": mempoolOld,
			})
		case "/v1/fs/ensure-dir":
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "boom"})
		case "/v1/compose/reconcile":
			composeWritten = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok", "changed": true,
			})
		case "/v1/compose/up":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	reg := service.NewTestRegistry([]model.ServiceTemplate{
		{
			ID: "mempool", ComposeDir: "/srv/truffels/compose/mempool",
			EnsureDirs: []model.EnsureDir{
				{Path: "/srv/truffels/data/mempool/cache", UID: 1000, GID: 1000, Mode: "0755"},
			},
		},
	})
	store := &captureAlertStore{}

	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL), store)
	_ = reconciler.Run()

	if !composeWritten {
		t.Error("compose should be written even when ensure-dir fails")
	}
	if len(store.alerts) == 0 {
		t.Error("expected ensure-dir failure to emit an alert")
	}
}

// A rewritten image tag has to survive reconciliation. The reconciler renders
// the compose file from a template every cycle, so if it did not read the tag
// back off the file on disk it would quietly undo every retag the update path
// makes — and ckpool would be pinned at v1.0.0 forever by a second mechanism.
func TestReconciler_KeepsRewrittenBuildTag(t *testing.T) {
	cases := []struct {
		serviceID string
		before    string
		after     string
	}{
		{"ckpool", "truffels/ckpool:v1.0.0", "truffels/ckpool:v1.3.0"},
		{"ckstats", "truffels/ckstats:latest", "truffels/ckstats:8f2e7c2f8403"},
	}

	for _, tc := range cases {
		t.Run(tc.serviceID, func(t *testing.T) {
			// What the file looks like after the update path retagged it.
			params, err := ExtractParams(tc.serviceID, renderFor(t, tc.serviceID, tc.after))
			if err != nil {
				t.Fatalf("extract params: %v", err)
			}
			rendered, err := Render(tc.serviceID, params)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(rendered, "image: "+tc.after) {
				t.Errorf("reconciliation dropped the rewritten tag; wanted %q in:\n%s", tc.after, rendered)
			}
			if strings.Contains(rendered, "image: "+tc.before) {
				t.Errorf("reconciliation put the old tag %q back:\n%s", tc.before, rendered)
			}

			// And it is a fixed point: reconciling the result changes nothing.
			params2, err := ExtractParams(tc.serviceID, rendered)
			if err != nil {
				t.Fatalf("extract params (2nd pass): %v", err)
			}
			again, err := Render(tc.serviceID, params2)
			if err != nil {
				t.Fatalf("render (2nd pass): %v", err)
			}
			if again != rendered {
				t.Errorf("reconciliation is not stable across cycles for %s", tc.serviceID)
			}
		})
	}
}

// renderFor produces the on-disk compose file for a custom-built service at the
// given image ref, going through the same template the reconciler writes.
func renderFor(t *testing.T, serviceID, imageRef string) string {
	t.Helper()
	var params any
	switch serviceID {
	case "ckpool":
		params = CkpoolParams{ImageTag: imageRef}
	case "ckstats":
		params = CkstatsParams{CkstatsImageTag: imageRef, DBImageTag: "postgres:16.14-alpine"}
	default:
		t.Fatalf("unknown service %q", serviceID)
	}
	out, err := Render(serviceID, params)
	if err != nil {
		t.Fatalf("render %s: %v", serviceID, err)
	}
	return out
}
