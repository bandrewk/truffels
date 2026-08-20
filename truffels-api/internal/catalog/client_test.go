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
