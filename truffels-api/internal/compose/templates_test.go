package compose

import (
	"os"
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
	// ckpool is built locally, and this template is what the reconciler
	// enforces on every API start. Without a build section
	// "docker compose build --build-arg SOURCE_REF=..." has nothing to build,
	// exits 0, and the update engine verifies the label of the *old* image —
	// i.e. every ckpool update silently becomes a no-op that reports success.
	assertContains(t, got, "build:")
	assertContains(t, got, "context: /srv/truffels/compose/ckpool")
	assertContains(t, got, "dockerfile: /srv/truffels/compose/ckpool/Dockerfile")
}

// The installed compose file and the reconciled template must declare the same
// build, otherwise the first API boot after an install silently rewrites the
// build definition (or, worse, drops it).
func TestRender_CkpoolBuildMatchesInstaller(t *testing.T) {
	installer, err := os.ReadFile("../../../install.sh")
	if err != nil {
		t.Skipf("installer not readable from this checkout: %v", err)
	}
	got, err := Render("ckpool", CkpoolParams{ImageTag: "truffels/ckpool:v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"context: /srv/truffels/compose/ckpool",
		"dockerfile: /srv/truffels/compose/ckpool/Dockerfile",
	} {
		if !strings.Contains(string(installer), line) {
			t.Errorf("install.sh does not declare %q for ckpool", line)
		}
		assertContains(t, got, line)
	}
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
	// The healthcheck's exit status must reflect wget alone, not wget-&& rm.
	// Capture the real status before cleanup so a failed rm (e.g. read-only or
	// mis-owned cache dir) can never flip a passing check to failing.
	// $$ (not $) because this is a docker-compose file: compose interpolates
	// bare $VAR/$? itself, so a literal $ must be written as $$ in the
	// template or compose silently blanks "rc" and mangles "$?" before the
	// shell ever sees it — asserting the doubled form pins that behavior.
	assertContains(t, got, "rc=$$?")
	assertContains(t, got, "exit $$rc")
	if strings.Contains(got, "&& rm -f /backend/cache/.starting || exit 1") {
		t.Error("healthcheck must not couple rm's exit status to service health")
	}
	// ...and the removal must happen ONLY on a successful probe. Docker runs
	// the healthcheck during start_period as well (those runs merely don't
	// count toward `retries`), so an unconditional rm deletes the sentinel at
	// the first probe — t≈30s, while the backend is still parsing an
	// oversized rbfcache.json — and the boot guard is then inert for exactly
	// the slow-start OOM loop it exists to break. Pinning the `if` keeps both
	// properties at once: conditional removal, and an rm whose own exit
	// status can never flip the verdict.
	assertContains(t, got, "if [ $$rc -eq 0 ]; then rm -f /backend/cache/.starting")
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

// The Dockerfile reconciler runs in the *API* container: it reads the expected
// Dockerfile from /repo and writes it into the deployed compose dir. Without
// this mount every reconcile logged "open /repo/dockerfiles/ckpool/Dockerfile:
// no such file or directory" and the deployed Dockerfiles silently stayed on
// whatever the installer wrote months earlier. That is how ckpool got rebuilt
// from a Dockerfile with no ARG SOURCE_REF and no source-ref LABEL, producing
// an unlabelled image that the update engine then correctly rejected with
// `built ref "" does not match requested "v1.2.0"`. Read-only is enough — the
// reconciler only reads; only the agent's self-update checkout writes.
func TestRender_TruffelsAPIMountsRepoReadOnly(t *testing.T) {
	got, err := Render("truffels", TruffelsParams{
		AgentTag: "truffels/agent:v0.3.1-dev.25",
		APITag:   "truffels/api:v0.3.1-dev.25",
		WebTag:   "truffels/web:v0.3.1-dev.25",
		RepoSrc:  "/home/truffel/Project-Truffels",
		Version:  "v0.3.1-dev.25",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, serviceBlock(t, got, "api"), "- /home/truffel/Project-Truffels:/repo:ro")
	// And the agent keeps write access — its self-update checkout needs it.
	assertContains(t, serviceBlock(t, got, "agent"), "- /home/truffel/Project-Truffels:/repo:rw")
}

// The installed compose file and the reconciled template must agree, otherwise
// the first API boot after an install rewrites what the installer just wrote —
// or a fresh install runs for a whole release cycle without the mount.
func TestInstaller_TruffelsAPIMountsRepoReadOnly(t *testing.T) {
	installer, err := os.ReadFile("../../../install.sh")
	if err != nil {
		t.Skipf("installer not readable from this checkout: %v", err)
	}
	assertContains(t, serviceBlock(t, string(installer), "api"), "- $TRUFFELS_REPO_SRC:/repo:ro")
	assertContains(t, serviceBlock(t, string(installer), "agent"), "- $TRUFFELS_REPO_SRC:/repo:rw")
}

// ckstats' SOURCE_REF is a pure stamp — unlike ckpool's, it drives no
// git clone --branch. A default of "unknown" therefore produced an image
// labelled with the literal string "unknown", and the installer built it
// without passing the arg at all. That is a fabricated version, and it
// defeats the rule that an unprovable build must report an empty version.
// Empty default plus an explicit ref from the installer, or nothing.
func TestCkstatsDockerfile_DoesNotDefaultToAPlaceholderRef(t *testing.T) {
	data, err := os.ReadFile("../../../dockerfiles/ckstats/Dockerfile")
	if err != nil {
		t.Skipf("dockerfile not readable from this checkout: %v", err)
	}
	df := string(data)
	if !strings.Contains(df, "ARG SOURCE_REF=\n") {
		t.Errorf("ckstats Dockerfile must default SOURCE_REF to empty; got:\n%s",
			grepLines(df, "SOURCE_REF"))
	}
	for _, bad := range []string{"ARG SOURCE_REF=unknown", "ARG SOURCE_REF=none", "ARG SOURCE_REF=latest"} {
		if strings.Contains(df, bad) {
			t.Errorf("ckstats Dockerfile stamps a placeholder ref: %q", bad)
		}
	}
	// The label itself must still be emitted, otherwise a build stamps nothing
	// at all and no update can ever prove itself.
	assertContains(t, df, "LABEL org.truffels.source-ref=$SOURCE_REF")
}

// A fresh install must stamp the commit it actually built, so a new device is
// honest from the first check rather than merely "not wrong".
func TestInstaller_BuildsCkstatsWithItsRealRef(t *testing.T) {
	installer, err := os.ReadFile("../../../install.sh")
	if err != nil {
		t.Skipf("installer not readable from this checkout: %v", err)
	}
	sh := string(installer)
	assertContains(t, sh, "--build-arg SOURCE_REF=")
	// The engine compares against a 12-char short SHA (checkGitHub truncates to
	// SHA[:12]). Stamping a full 40-char hash would mismatch forever.
	assertContains(t, sh, "rev-parse --short=12 HEAD")
}

// grepLines returns the lines of s containing substr, for readable failures.
func grepLines(s, substr string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// serviceBlock returns just the given service's block from a compose document.
// Asserting against the whole document would let one service's mount satisfy
// an assertion about another's — exactly the mistake that hid the missing api
// /repo mount, since the agent block carries a matching line.
func serviceBlock(t *testing.T, doc, name string) string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	start := -1
	for i, l := range lines {
		if l == "  "+name+":" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no %q service block found in:\n%s", name, doc)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if len(lines[i])-len(strings.TrimLeft(lines[i], " ")) <= 2 {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q, got:\n%s", substr, s)
	}
}
