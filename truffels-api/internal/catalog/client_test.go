package catalog

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Remove must use its own (long) timeout, independent of the quick read
// timeout: a slow `compose down` on the agent should not be cut short by the
// client. Regression for the phantom-install bug where a 10s client timeout
// fired before the agent finished tearing a chain node down.
func TestRemoveUsesItsOwnTimeout(t *testing.T) {
	var gotRemove bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/service/remove" {
			gotRemove = true
			time.Sleep(120 * time.Millisecond) // slower than readTimeout, faster than removeTimeout
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	// Squeeze the durations so the test runs fast while preserving the ordering
	// that matters: read < the slow response < remove.
	c.readTimeout = 30 * time.Millisecond
	c.removeTimeout = 2 * time.Second

	if err := c.Remove("digibyted", false); err != nil {
		t.Fatalf("Remove should tolerate a 120ms teardown under a 2s timeout, got: %v", err)
	}
	if !gotRemove {
		t.Fatal("agent remove endpoint was never called")
	}
}

// A remove that genuinely exceeds its own timeout still fails, so the caller
// can react rather than hang forever.
func TestRemoveTimesOutWhenExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.removeTimeout = 40 * time.Millisecond
	if err := c.Remove("digibyted", false); err == nil {
		t.Fatal("Remove should fail when the teardown exceeds removeTimeout")
	}
}
