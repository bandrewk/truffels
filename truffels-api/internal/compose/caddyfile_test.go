package compose

import (
	"strings"
	"testing"
)

func TestRenderCaddyfile_HasStaticHealthRoute(t *testing.T) {
	got := RenderCaddyfile()
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
