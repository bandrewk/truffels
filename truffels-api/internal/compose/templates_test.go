package compose

import (
	"strings"
	"testing"
)

func TestRender_Bitcoin(t *testing.T) {
	got, err := Render("bitcoind", BitcoinParams{ImageTag: "btcpayserver/bitcoin:30.2"})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: btcpayserver/bitcoin:30.2")
	assertContains(t, got, "container_name: truffels-bitcoind")
	assertContains(t, got, "memory: 3500M")
}

func TestRender_Electrs(t *testing.T) {
	got, err := Render("electrs", ElectrsParams{ImageTag: "getumbrel/electrs:v0.11.0"})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: getumbrel/electrs:v0.11.0")
	assertContains(t, got, "container_name: truffels-electrs")
	assertContains(t, got, "memory: 2048M")
}

func TestRender_Ckpool(t *testing.T) {
	got, err := Render("ckpool", CkpoolParams{ImageTag: "truffels/ckpool:v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: truffels/ckpool:v1.0.0")
	assertContains(t, got, "memory: 1024M")
	assertContains(t, got, "container_name: truffels-ckpool")
}

func TestRender_Mempool(t *testing.T) {
	got, err := Render("mempool", MempoolParams{
		BackendImageTag:  "mempool/backend:v3.3.1",
		FrontendImageTag: "mempool/frontend:v3.3.1",
		DBImageTag:       "mariadb:lts@sha256:abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: mempool/backend:v3.3.1")
	assertContains(t, got, "image: mempool/frontend:v3.3.1")
	assertContains(t, got, "image: mariadb:lts@sha256:abc123")
	assertContains(t, got, "container_name: truffels-mempool-backend")
	// Backend OOM-fix shape: bind-mounted cache, bumped heap + cgroup
	// (raised in v0.3.1-dev.21 after rbfcache.json runaway hit the V8 heap
	// ceiling at 1792 MB on mainnet), healthcheck.
	assertContains(t, got, "/srv/truffels/data/mempool/cache:/backend/cache")
	assertContains(t, got, "--max-old-space-size=2560")
	assertContains(t, got, "memory: 3072M")
	assertContains(t, got, "start_period: 300s")
	// Frontend healthcheck.
	assertContains(t, got, "http://127.0.0.1:8080/")
	// Boot guard: a start that never became healthy leaves the sentinel behind,
	// so the next start clears the cache instead of OOMing on the same file.
	assertContains(t, got, "/backend/cache/.starting")
	assertContains(t, got, "exec /backend/start.sh")
	// Must wrap start.sh, not node — start.sh renders mempool-config.json from
	// the MEMPOOL_* env vars, so wrapping node would drop all configuration.
	if strings.Contains(got, "exec node ") {
		t.Error("boot guard must exec /backend/start.sh, not node directly")
	}
	assertContains(t, got, "rm -f /backend/cache/.starting")
}

func TestRender_Ckstats(t *testing.T) {
	got, err := Render("ckstats", CkstatsParams{
		CkstatsImageTag: "truffels/ckstats:latest",
		DBImageTag:       "postgres:16.13-alpine",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: truffels/ckstats:latest")
	assertContains(t, got, "image: postgres:16.13-alpine")
	assertContains(t, got, "container_name: truffels-ckstats-db")
	// dev.16: ckstats-cron now has a healthcheck (sentinel-file mtime probe)
	// so a wedged loop is visible instead of silently "running".
	assertContains(t, got, "cron-last-run")
	assertContains(t, got, "start_period: 120s")
}

func TestRender_Proxy(t *testing.T) {
	got, err := Render("proxy", ProxyParams{ImageTag: "caddy:2.11.2-alpine"})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, got, "image: caddy:2.11.2-alpine")
	assertContains(t, got, "container_name: truffels-proxy")
	assertContains(t, got, "memory: 128M")
	// dev.15: healthcheck must hit a path that doesn't depend on any upstream
	// (the Caddyfile's catch-all reverse-proxies to mempool; using "/" caused
	// Caddy to be marked unhealthy whenever mempool was stopped).
	assertContains(t, got, "http://127.0.0.1:80/proxy-health")
}

func TestRender_UnknownService(t *testing.T) {
	_, err := Render("unknown", nil)
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

func TestRender_Truffels(t *testing.T) {
	got, err := Render("truffels", TruffelsParams{
		AgentTag: "truffels/agent:v0.3.1-dev.16",
		APITag:   "truffels/api:v0.3.1-dev.16",
		WebTag:   "truffels/web:v0.3.1-dev.16",
		RepoSrc:  "/home/truffel/Project-Truffels",
		Version:  "v0.3.1-dev.16",
	})
	if err != nil {
		t.Fatal(err)
	}
	// dev.16: build.args.VERSION must be present so docker compose build sets
	// the ldflag + OCI label correctly even when buildkit drops --build-arg.
	assertContains(t, got, "VERSION: v0.3.1-dev.16")
	// The dev.15 mount fix is the whole point — verify it's there.
	assertContains(t, got, "/srv/truffels/data:/srv/truffels/data:rw")
	assertContains(t, got, "TRUFFELS_DATA_ROOT")
	// And config flipped from :ro to :rw so the proxy Caddyfile can be reconciled.
	assertContains(t, got, "/srv/truffels/config:/srv/truffels/config:rw")
	assertContains(t, got, "TRUFFELS_CONFIG_ROOT")
	// Image tags from params land in the rendered output.
	assertContains(t, got, "image: truffels/agent:v0.3.1-dev.16")
	assertContains(t, got, "image: truffels/api:v0.3.1-dev.16")
	assertContains(t, got, "image: truffels/web:v0.3.1-dev.16")
	// Build contexts pick up RepoSrc.
	assertContains(t, got, "context: /home/truffel/Project-Truffels/truffels-agent")
	assertContains(t, got, "context: /home/truffel/Project-Truffels/truffels-api")
	assertContains(t, got, "context: /home/truffel/Project-Truffels/truffels-web")
	// /repo mount on agent for source access during build.
	assertContains(t, got, "/home/truffel/Project-Truffels:/repo:rw")
	// Container names.
	assertContains(t, got, "container_name: truffels-agent")
	assertContains(t, got, "container_name: truffels-api")
	assertContains(t, got, "container_name: truffels-web")
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q, got:\n%s", substr, s)
	}
}
