package catalog

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientFetchesAndCaches(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"digibyted":{"schema_version":1,"id":"digibyted",
		  "display_name":"DigiByte Core","description":"d","role":"chain-node",
		  "implementation":"digibyte-core","chain":"dgb","containers":[],
		  "resources":{"memory_floor_mb":1024}}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	got, err := c.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if got["digibyted"].Implementation != "digibyte-core" {
		t.Errorf("Implementation = %q", got["digibyted"].Implementation)
	}
	if _, err := c.All(); err != nil {
		t.Fatalf("second All: %v", err)
	}
	if calls != 1 {
		t.Errorf("Agent was called %d times, expected 1 (cache)", calls)
	}
}

func TestClientGetUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, ok := c.Get("no-such-id"); ok {
		t.Error("Get returned ok=true for unknown id")
	}
}

func TestEntryContainerNamesFromField(t *testing.T) {
	// Entry with container_names field set should return that field, no derivation.
	entry := Entry{
		ID: "test-id",
		Containers: []struct {
			Name          string `json:"name"`
			MemoryLimitMB int    `json:"memory_limit_mb"`
		}{
			{Name: "node", MemoryLimitMB: 1024},
		},
		ContainerNamesField: []string{"truffels-test-id-node"},
	}
	got := entry.ContainerNames()
	if len(got) != 1 || got[0] != "truffels-test-id-node" {
		t.Errorf("ContainerNames() = %v, expected [truffels-test-id-node]", got)
	}
}

func TestEntryContainerNamesWithoutFieldNoDerivation(t *testing.T) {
	// Entry without container_names field should return empty/nil, not derived names.
	// This tests the key change: no more self-derivation in the API.
	entry := Entry{
		ID: "test-id",
		Containers: []struct {
			Name          string `json:"name"`
			MemoryLimitMB int    `json:"memory_limit_mb"`
		}{
			{Name: "node", MemoryLimitMB: 1024},
		},
		ContainerNamesField: nil,
	}
	got := entry.ContainerNames()
	if len(got) != 0 {
		t.Errorf("ContainerNames() = %v, expected empty when field not set (no derivation)", got)
	}
}

func TestClientIDs(t *testing.T) {
	// IDs() should return a map with all catalog entry IDs
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"digibyted":{"schema_version":1,"id":"digibyted",
		  "display_name":"DigiByte Core","description":"d","role":"chain-node",
		  "implementation":"digibyte-core","chain":"dgb","containers":[],
		  "resources":{"memory_floor_mb":1024}}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	ids, err := c.IDs()
	if err != nil {
		t.Fatalf("IDs: %v", err)
	}
	if !ids["digibyted"] {
		t.Errorf("IDs should contain digibyted, got %v", ids)
	}
	if len(ids) != 1 {
		t.Errorf("IDs() = %d entries, expected 1", len(ids))
	}
	// Verify it uses the cache
	if _, err := c.IDs(); err != nil {
		t.Fatalf("second IDs: %v", err)
	}
	if calls != 1 {
		t.Errorf("Agent was called %d times, expected 1 (cache)", calls)
	}
}
