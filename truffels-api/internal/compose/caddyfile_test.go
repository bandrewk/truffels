package compose

import (
	"strings"
	"testing"
)

func TestRenderCaddyfile_HasStaticHealthRoute(t *testing.T) {
	got := RenderCaddyfile(nil)
	if !strings.Contains(got, "handle /proxy-health") {
		t.Error("missing /proxy-health route")
	}
	if !strings.Contains(got, `respond "OK" 200`) {
		t.Error("missing static OK response on /proxy-health")
	}
	// Existing reverse_proxy routes must still be present.
	if !strings.Contains(got, "handle /admin*") {
		t.Error("missing /admin* route")
	}
	if !strings.Contains(got, "handle /api/truffels/*") {
		t.Error("missing /api/truffels/* route")
	}
	if !strings.Contains(got, "handle /ckstats*") {
		t.Error("missing /ckstats* route")
	}
}

// The proxy is the only component that ever sees a client's real address —
// everything behind it logs this proxy's container IP — so without an access
// log that address exists nowhere and "did that device reach us" cannot be
// answered. It could not be answered in dev.29, which is why this is pinned.
func TestRenderCaddyfile_LogsRequests(t *testing.T) {
	got := RenderCaddyfile(nil)

	for _, want := range []string{"log {", "output stdout", "format json"} {
		if !strings.Contains(got, want) {
			t.Errorf("Caddyfile is missing %q — requests would go unlogged", want)
		}
	}
}

// The healthcheck runs every 30s. Logging it writes ~2900 lines a day and
// buries the traffic the log exists to show, so the health route opts out —
// and it has to be inside that handle block, not merely present in the file.
func TestRenderCaddyfile_HealthRouteOptsOutOfLogging(t *testing.T) {
	got := RenderCaddyfile(nil)

	start := strings.Index(got, "handle /proxy-health {")
	if start < 0 {
		t.Fatal("no /proxy-health handle block")
	}
	end := strings.Index(got[start:], "\n\t}")
	if end < 0 {
		t.Fatal("/proxy-health handle block is not closed")
	}
	block := got[start : start+end]

	if !strings.Contains(block, "log_skip") {
		t.Errorf("the health route does not skip logging:\n%s", block)
	}
}
