package compose

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ckstats is served under /ckstats by Caddy *without* `uri strip_prefix`, so
// Next.js must carry the prefix itself via basePath. Until v0.3.1-dev.26 that
// line existed only as a hand edit in the working copy at
// /srv/truffels/data/ckpoolstats — it appeared in no file in this repo. Two
// things followed:
//
//  1. Every fresh install shipped a broken ckstats. install.sh clones a
//     pristine upstream tree; nothing added basePath; assets were emitted at
//     /_next/... and swallowed by Caddy's mempool catch-all.
//  2. The hand edit left the working copy dirty, so `git checkout <commit>`
//     refused and no ckstats update could ever apply — the
//     "git checkout failed: exit status 1" the update engine reported.
//
// The fix moves the patch into the build, next to the fetch() rewrites that
// were already there. These tests execute the *actual* RUN command from the
// Dockerfile against real upstream next.config.js snapshots, so they fail if
// the patch stops applying — not merely if its text changes.
const ckstatsPatchBeginMarker = "# truffels-patch:basePath:begin"
const ckstatsPatchEndMarker = "# truffels-patch:basePath:end"

// extractCkstatsBasePathPatch pulls the shell body of the marked RUN
// instruction out of the ckstats Dockerfile, unfolding backslash
// continuations, so a test can run it verbatim under /bin/sh.
func extractCkstatsBasePathPatch(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../../dockerfiles/ckstats/Dockerfile")
	if err != nil {
		t.Skipf("dockerfile not readable from this checkout: %v", err)
	}
	lines := strings.Split(string(data), "\n")

	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == ckstatsPatchBeginMarker {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("ckstats Dockerfile has no %q marker; the basePath patch must stay\n"+
			"machine-locatable so this test exercises the real command", ckstatsPatchBeginMarker)
	}

	var body []string
	sawRun := false
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == ckstatsPatchEndMarker {
			break
		}
		if !sawRun {
			if !strings.HasPrefix(l, "RUN ") {
				t.Fatalf("expected a RUN instruction after %q, got %q", ckstatsPatchBeginMarker, l)
			}
			sawRun = true
			l = strings.TrimPrefix(l, "RUN ")
		}
		body = append(body, strings.TrimSuffix(strings.TrimRight(l, " "), "\\"))
	}
	if !sawRun {
		t.Fatalf("no RUN instruction between the basePath patch markers")
	}
	return strings.Join(body, "\n")
}

// runCkstatsBasePathPatch executes the extracted patch in dir, the way the
// builder does inside /app.
func runCkstatsBasePathPatch(t *testing.T, dir string) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", extractCkstatsBasePathPatch(t))
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ckstatsFixtures are verbatim upstream next.config.js snapshots. The
// pre-next15 one is what the device was built from; 8f2e7c2f8403 is the
// upstream head the pending update targets, which renamed
// experimental.serverComponentsExternalPackages to serverExternalPackages.
// The patch must land on both — a fix that only works against the revision we
// happen to be sitting on is exactly the bug being fixed here.
var ckstatsFixtures = []string{
	"next.config.pre-next15.js",
	"next.config.8f2e7c2f8403.js",
}

func seedCkstatsFixture(t *testing.T, fixture string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "ckstats", fixture))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "next.config.js"), src, 0o644); err != nil {
		t.Fatalf("seed fixture %s: %v", fixture, err)
	}
	return dir
}

func readCkstatsConfig(t *testing.T, dir string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "next.config.js"))
	if err != nil {
		t.Fatalf("read patched config: %v", err)
	}
	return string(got)
}

// The core guarantee: a pristine upstream checkout comes out of the build step
// with basePath set, for every upstream revision we support.
func TestCkstatsDockerfile_InjectsBasePath(t *testing.T) {
	for _, fixture := range ckstatsFixtures {
		t.Run(fixture, func(t *testing.T) {
			dir := seedCkstatsFixture(t, fixture)
			if out, err := runCkstatsBasePathPatch(t, dir); err != nil {
				t.Fatalf("patch failed on %s: %v\n%s", fixture, err, out)
			}
			got := readCkstatsConfig(t, dir)
			if !strings.Contains(got, "basePath: '/ckstats',") {
				t.Errorf("patched %s has no basePath:\n%s", fixture, got)
			}
			// It has to sit inside the config object, not merely somewhere in
			// the file — a line appended after `module.exports` would satisfy a
			// naive grep and change nothing at runtime.
			idx := strings.Index(got, "const nextConfig = {")
			bp := strings.Index(got, "basePath:")
			exp := strings.Index(got, "module.exports")
			if idx < 0 || bp < idx || (exp >= 0 && bp > exp) {
				t.Errorf("basePath is not inside the nextConfig object in %s:\n%s", fixture, got)
			}
		})
	}
}

// The build re-runs on every image rebuild against a working copy that may
// already carry the previous run's output (the source is bind-mounted, not
// copied fresh). A second injection would produce a duplicate object key.
func TestCkstatsDockerfile_BasePathPatchIsIdempotent(t *testing.T) {
	for _, fixture := range ckstatsFixtures {
		t.Run(fixture, func(t *testing.T) {
			dir := seedCkstatsFixture(t, fixture)
			for pass := 1; pass <= 3; pass++ {
				if out, err := runCkstatsBasePathPatch(t, dir); err != nil {
					t.Fatalf("pass %d failed on %s: %v\n%s", pass, fixture, err, out)
				}
			}
			if n := strings.Count(readCkstatsConfig(t, dir), "basePath:"); n != 1 {
				t.Errorf("basePath appears %d times after 3 passes on %s, want exactly 1:\n%s",
					n, fixture, readCkstatsConfig(t, dir))
			}
		})
	}
}

// If upstream ever restructures next.config.js past the anchor, the build must
// stop with a diagnosable error. Silently building a prefix-less bundle is the
// failure mode that produced a blank dashboard on every fresh install and went
// unnoticed for months.
func TestCkstatsDockerfile_BasePathPatchFailsLoudlyOnUnknownLayout(t *testing.T) {
	dir := t.TempDir()
	unknown := "export default { webpack: (c) => c }\n"
	if err := os.WriteFile(filepath.Join(dir, "next.config.js"), []byte(unknown), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCkstatsBasePathPatch(t, dir)
	if err == nil {
		t.Fatalf("patch reported success on an unrecognised next.config.js; the build\n"+
			"would ship a prefix-less bundle. output:\n%s", out)
	}
	if !strings.Contains(out, "basePath") {
		t.Errorf("failure message should name basePath so the cause is obvious, got:\n%s", out)
	}
}

// Belt and braces: the working copy must stay a pristine upstream checkout, so
// no part of the repo may hand the operator a next.config.js to drop in.
// TROUBLESHOOTING.md is allowed to *mention* basePath (it documents the
// requirement) — what must not exist is a second place that applies it.
func TestCkstatsBasePath_IsAppliedOnlyAtBuildTime(t *testing.T) {
	data, err := os.ReadFile("../../../install.sh")
	if err != nil {
		t.Skipf("installer not readable from this checkout: %v", err)
	}
	if strings.Contains(string(data), "basePath") {
		t.Errorf("install.sh writes basePath into the working copy; that makes the\n"+
			"checkout dirty and blocks every later `git checkout <commit>`:\n%s",
			grepLines(string(data), "basePath"))
	}
}
