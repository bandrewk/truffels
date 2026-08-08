package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Characterization tests for the handlers whose only untested part was the
// external command they run: what gets executed, what the caller sees when it
// fails, and — for git checkout — what a successful run returns.
//
// These pin the observable surface (status code, error prefix, which keys are
// present) around every command site that the runCapture/runStdout refactor
// rewrites, so the same assertions hold before and after it.
//
// The suite runs in a container without a docker CLI, which is what makes the
// failure paths reachable at all. Where the outcome would differ on a host that
// does have docker, the assertion is written to hold in both worlds and the
// docker-less expectation is guarded by exec.LookPath.

func dockerMissing() bool {
	_, err := exec.LookPath("docker")
	return err != nil
}

// --- inspectContainer ---

func TestInspectContainer_ReportsNotFoundWhenTheCommandFails(t *testing.T) {
	cs := inspectContainer("truffels-bitcoind")

	if cs.Name != "truffels-bitcoind" {
		t.Errorf("name = %q, want the requested container", cs.Name)
	}
	if cs.Status == "" {
		t.Error("status must never be empty — every return path sets it")
	}
	if dockerMissing() {
		if cs.Status != "not_found" || cs.Health != "unknown" {
			t.Errorf("docker unavailable: got status=%q health=%q, want not_found/unknown",
				cs.Status, cs.Health)
		}
	}
}

// --- handleImagePull ---

func TestHandleImagePull_SurfacesCommandFailure(t *testing.T) {
	// A repository that cannot resolve, so the pull fails whether or not a
	// docker CLI is present.
	body, _ := json.Marshal(map[string]string{
		"image": "truffels/agent-test-nonexistent:v0.0.0",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/pull", bytes.NewReader(body))

	handleImagePull(w, r)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] == "" {
		t.Error("failure response must carry the command error")
	}
	if _, ok := resp["output"]; !ok {
		t.Error("failure response must carry the command output")
	}
}

// --- handleImageInspect ---

func TestHandleImageInspect_AllowedContainerReachesDocker(t *testing.T) {
	body, _ := json.Marshal(imageInspectRequest{Container: "truffels-bitcoind"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/inspect", bytes.NewReader(body))

	handleImageInspect(w, r)

	if w.Code == 400 || w.Code == 403 {
		t.Fatalf("allowed container must get past the guard, got %d: %s", w.Code, w.Body.String())
	}
	if w.Code == 500 {
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !strings.HasPrefix(resp["error"], "cannot inspect container: ") {
			t.Errorf("error = %q, want the 'cannot inspect container: ' prefix", resp["error"])
		}
	}
}

// --- handleImageTag ---

func TestHandleImageTag_AllowedRefsReachDockerTag(t *testing.T) {
	// Both refs pass isAllowedImageRef; the source image does not exist, so the
	// tag fails with or without a docker CLI.
	//
	// The repository has to be a real one now that isAllowedImageRef enumerates
	// them — the guard, not the missing image, would otherwise be what this
	// test observes. Nonexistence moved into the tag, which is unconstrained
	// beyond its charset.
	body, _ := json.Marshal(map[string]string{
		"source": "truffels/ckpool:agent-test-nonexistent-src",
		"target": "truffels/ckpool:agent-test-nonexistent-dst",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/tag", bytes.NewReader(body))

	handleImageTag(w, r)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp["error"], "docker tag failed: ") {
		t.Errorf("error = %q, want the 'docker tag failed: ' prefix", resp["error"])
	}
	if _, ok := resp["output"]; !ok {
		t.Error("failure response must carry the command output")
	}
}

// --- fetchDockerStorage ---

func TestFetchDockerStorage_ReturnsNilWhenTheCommandFails(t *testing.T) {
	items := fetchDockerStorage()

	if dockerMissing() && items != nil {
		t.Fatalf("docker unavailable: expected nil, got %v", items)
	}
	for _, it := range items {
		if it.Type == "" {
			t.Errorf("parsed row with empty type: %+v", it)
		}
	}
}

// --- fallbackContainerLogs ---

func TestFallbackContainerLogs_UnknownServiceReturnsEmpty(t *testing.T) {
	if got := fallbackContainerLogs(context.Background(), "not-a-service", 100, ""); got != "" {
		t.Fatalf("unknown service must yield no logs, got %q", got)
	}
}

func TestFallbackContainerLogs_PrefixesOnlyMultiContainerServices(t *testing.T) {
	// mempool has two containers, so every line is tagged with its origin.
	multi := fallbackContainerLogs(context.Background(), "mempool", 100, "")
	for _, line := range strings.Split(multi, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "truffels-mempool-") {
			t.Errorf("multi-container line is missing its container prefix: %q", line)
		}
	}

	// bitcoind has one, so nothing is prefixed.
	single := fallbackContainerLogs(context.Background(), "bitcoind", 100, "")
	for _, line := range strings.Split(single, "\n") {
		if strings.HasPrefix(line, "truffels-bitcoind  | ") {
			t.Errorf("single-container line must not be prefixed: %q", line)
		}
	}
}

// --- handleGitCheckout, past the validators ---

// allowRepoDir registers a temp repo as checkout-able for one test, the same
// way registerResettable does for the reset path. It is deliberately NOT added
// to resettableRepoDirs: that keeps these tests on the /repo code path, where
// nothing is reset and nothing is removed.
func allowRepoDir(t *testing.T, dir string) {
	t.Helper()
	allowedRepoDirs[dir] = true
	t.Cleanup(func() { delete(allowedRepoDirs, dir) })
}

func TestHandleGitCheckout_SurfacesFetchFailure(t *testing.T) {
	dir := t.TempDir() // allowed, but not a git repository
	allowRepoDir(t, dir)

	body, _ := json.Marshal(gitCheckoutRequest{RepoDir: dir, Tag: "v0.0.1"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/git/checkout", bytes.NewReader(body))

	handleGitCheckout(w, r)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp["error"], "git fetch failed: ") {
		t.Errorf("error = %q, want the 'git fetch failed: ' prefix", resp["error"])
	}
	if resp["output"] == "" {
		t.Error("the git output is what tells the operator why it failed — it must be returned")
	}
}

func TestHandleGitCheckout_ChecksOutTagFromLocalRemote(t *testing.T) {
	// A real fetch and checkout end to end. The remote is a directory on local
	// disk, so nothing touches the network.
	upstream := t.TempDir()
	git(t, upstream, "init", "-q", "-b", "main")
	git(t, upstream, "config", "user.email", "t@example.invalid")
	git(t, upstream, "config", "user.name", "t")
	write(t, upstream, "README.md", "one\n")
	git(t, upstream, "add", "README.md")
	git(t, upstream, "commit", "-q", "-m", "first")
	git(t, upstream, "tag", "v0.0.1")

	work := t.TempDir()
	git(t, work, "clone", "-q", upstream, work)
	allowRepoDir(t, work)

	body, _ := json.Marshal(gitCheckoutRequest{RepoDir: work, Tag: "v0.0.1"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/git/checkout", bytes.NewReader(body))

	handleGitCheckout(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "ok" {
		t.Fatalf("status = %q, want ok", resp["status"])
	}
	if _, ok := resp["output"]; !ok {
		t.Error("success response must still carry the git output")
	}

	head := strings.TrimSpace(git(t, work, "rev-parse", "HEAD"))
	tagged := strings.TrimSpace(git(t, upstream, "rev-parse", "v0.0.1^{commit}"))
	if head != tagged {
		t.Errorf("HEAD = %s, want the tagged commit %s", head, tagged)
	}
}

// A ref and a pathspec are not the same kind of argument, and `git checkout`
// accepts both in the same position. Without the `--` terminator, a request for
// a ref that does not exist but happens to name a tracked *file* is answered by
// restoring that file — exit status 0, HEAD unmoved, and this endpoint replying
// {"status":"ok"}. The update engine takes that at its word and builds, labels
// and ships an image from the wrong commit.
//
// The validators do not help here: "v0.0.9" passes isValidTag. Nothing about
// this is exotic; it is the ordinary meaning of the argv git was given.
func TestHandleGitCheckout_RefusesARefThatOnlyNamesAFile(t *testing.T) {
	upstream := t.TempDir()
	git(t, upstream, "init", "-q", "-b", "main")
	git(t, upstream, "config", "user.email", "t@example.invalid")
	git(t, upstream, "config", "user.name", "t")
	write(t, upstream, "README.md", "one\n")
	// A tracked file whose name is a valid tag, and no tag of that name.
	write(t, upstream, "v0.0.9", "committed\n")
	git(t, upstream, "add", "README.md", "v0.0.9")
	git(t, upstream, "commit", "-q", "-m", "first")

	work := t.TempDir()
	git(t, work, "clone", "-q", upstream, work)
	allowRepoDir(t, work)
	head := strings.TrimSpace(git(t, work, "rev-parse", "HEAD"))

	// Local content that a path-checkout would silently discard.
	write(t, work, "v0.0.9", "local\n")

	body, _ := json.Marshal(gitCheckoutRequest{RepoDir: work, Tag: "v0.0.9"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/git/checkout", bytes.NewReader(body))

	handleGitCheckout(w, r)

	if w.Code != 500 {
		t.Fatalf("expected 500 for a ref that does not exist, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp["error"], "git checkout failed: ") {
		t.Errorf("error = %q, want the 'git checkout failed: ' prefix", resp["error"])
	}
	if got := strings.TrimSpace(git(t, work, "rev-parse", "HEAD")); got != head {
		t.Errorf("HEAD moved to %s; a refused checkout must change nothing", got)
	}
	content, err := os.ReadFile(filepath.Join(work, "v0.0.9"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "local\n" {
		t.Errorf("v0.0.9 = %q; the refused checkout overwrote it as a pathspec", content)
	}
}

// The commit-hash scheme end to end, with a real 40-char object name. The `--`
// terminator has to leave this working too — it is the ref form every ckstats
// update uses.
func TestHandleGitCheckout_ChecksOutCommitUnderCommitScheme(t *testing.T) {
	upstream := t.TempDir()
	git(t, upstream, "init", "-q", "-b", "main")
	git(t, upstream, "config", "user.email", "t@example.invalid")
	git(t, upstream, "config", "user.name", "t")
	write(t, upstream, "README.md", "one\n")
	git(t, upstream, "add", "README.md")
	git(t, upstream, "commit", "-q", "-m", "first")
	first := strings.TrimSpace(git(t, upstream, "rev-parse", "HEAD"))
	write(t, upstream, "README.md", "two\n")
	git(t, upstream, "add", "README.md")
	git(t, upstream, "commit", "-q", "-m", "second")

	work := t.TempDir()
	git(t, work, "clone", "-q", upstream, work)
	allowRepoDir(t, work)

	body, _ := json.Marshal(gitCheckoutRequest{RepoDir: work, Tag: first, RefScheme: "commit"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/git/checkout", bytes.NewReader(body))

	handleGitCheckout(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(git(t, work, "rev-parse", "HEAD")); got != first {
		t.Errorf("HEAD = %s, want %s", got, first)
	}
}
