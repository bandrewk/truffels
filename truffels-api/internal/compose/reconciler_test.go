package compose

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL))
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

	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL))
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

	reconciler := NewReconciler(reg, docker.NewComposeClient(srv.URL))
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

		default:
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"error":"not found: %s"}`, r.URL.Path)
		}
	}))
}

func newMockAgentError(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "agent unavailable"})
	}))
}
