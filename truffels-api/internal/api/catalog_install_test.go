package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"truffels-api/internal/catalog"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
)

func newCatalogTestServerWithAgent(t *testing.T, agentHandler http.HandlerFunc) *Server {
	srv, st := newTestServer(t)

	// Mock Agent
	if agentHandler == nil {
		agentHandler = func(w http.ResponseWriter, r *http.Request) {}
	}
	agent := httptest.NewServer(agentHandler)
	t.Cleanup(func() { agent.Close() })
	srv.catalogClient = catalog.NewClient(agent.URL)
	srv.registry = service.NewRegistry("/srv/truffels/compose", "", "", srv.catalogClient, st)

	srv.collector = nil

	// Pre-fill some snapshots for P95 (requires 2000 MB)
	_ = st.InsertContainerSnapshots([]model.ContainerSnapshot{
		{Timestamp: time.Now(), Container: "node", MemUsageMB: 2000},
	})

	return srv
}

func TestCatalogInstallFailsUnknownID(t *testing.T) {
	srv := newCatalogTestServerWithAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/catalog" {
			_, _ = w.Write([]byte(`{}`))
		}
	})
	req := authenticatedRequest(t, srv, "POST", "/api/truffels/catalog/unknown/install", `{"params":{}}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCatalogInstallAdmissionFail(t *testing.T) {
	calledApply := false
	srv := newCatalogTestServerWithAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/catalog" {
			_, _ = w.Write([]byte(`{"testd":{"id":"testd","resources":{"memory_floor_mb":8000}}}`)) // Needs 8GB, we have 4GB
		}
		if r.URL.Path == "/v1/service/apply" {
			calledApply = true
		}
	})

	req := authenticatedRequest(t, srv, "POST", "/api/truffels/catalog/testd/install", `{"params":{}}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
	if calledApply {
		t.Errorf("agent apply should not be called on admission fail")
	}
	_, ok, _ := srv.store.GetCatalogInstallation("testd")
	if ok {
		t.Errorf("expected no DB entry")
	}
}

func TestCatalogInstallSuccess(t *testing.T) {
	applyCalled := false
	srv := newCatalogTestServerWithAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/catalog" {
			_, _ = w.Write([]byte(`{"testd":{"id":"testd","resources":{"memory_floor_mb":100}}}`))
		}
		if r.URL.Path == "/v1/service/apply" {
			applyCalled = true
			w.WriteHeader(http.StatusOK)
		}
	})

	req := authenticatedRequest(t, srv, "POST", "/api/truffels/catalog/testd/install", `{"params":{}}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !applyCalled {
		t.Errorf("agent apply not called")
	}
	_, ok, _ := srv.store.GetCatalogInstallation("testd")
	if !ok {
		t.Errorf("expected DB entry")
	}
}

func TestCatalogUninstall(t *testing.T) {
	srv := newCatalogTestServerWithAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/service/remove" {
			w.WriteHeader(http.StatusOK)
		}
	})

	// uninstall not installed
	req := authenticatedRequest(t, srv, "POST", "/api/truffels/catalog/testd/uninstall", `{"purge_data":true}`)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	// install then uninstall
	_ = srv.store.AddCatalogInstallation("testd", "testd", map[string]any{})
	req = authenticatedRequest(t, srv, "POST", "/api/truffels/catalog/testd/uninstall", `{"purge_data":true}`)
	w = httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	_, ok, _ := srv.store.GetCatalogInstallation("testd")
	if ok {
		t.Errorf("still in db")
	}
}
