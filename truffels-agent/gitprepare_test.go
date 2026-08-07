package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- prepareBuildSourceForCheckout ---
//
// Background: the ckstats build source at /srv/truffels/data/ckpoolstats was
// stuck. `git checkout <commit>` refused because the tree carried 62 files
// whose mode had flipped 100644 -> 100755, a pnpm-lock.yaml left over from a
// local `pnpm install`, and a hand-added basePath in next.config.js. Every
// ckstats update failed with "git checkout failed: exit status 1".
//
// These tests exercise the real git commands against real repositories built
// in t.TempDir(). The load-bearing property is not that the tree ends up clean
// — it is *what survives*: untracked files must never be removed, because the
// same handler is reachable for /repo, which is the user's own checkout.

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newSourceRepo builds a two-commit repo standing in for the ckstats upstream
// checkout, and returns the repo path plus the first commit's hash.
func newSourceRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.invalid")
	git(t, dir, "config", "user.name", "t")

	write(t, dir, "next.config.js", "const nextConfig = {}\n")
	write(t, dir, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	git(t, dir, "add", "next.config.js", "pnpm-lock.yaml")
	git(t, dir, "commit", "-q", "-m", "first")
	first := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	write(t, dir, "next.config.js", "const nextConfig = { v: 2 }\n")
	git(t, dir, "add", "next.config.js")
	git(t, dir, "commit", "-q", "-m", "second")

	return dir, first
}

// The bug itself: a tree dirtied the way the device's was must become
// checkout-able.
func TestPrepareBuildSource_UnblocksCheckout(t *testing.T) {
	dir, first := newSourceRepo(t)

	// Reproduce the device's state: a hand edit and a regenerated lockfile.
	write(t, dir, "next.config.js", "const nextConfig = {\n  basePath: '/ckstats',\n}\n")
	write(t, dir, "pnpm-lock.yaml", "lockfileVersion: 9\nchanged: true\n")

	// Baseline: checkout must fail, or this test proves nothing.
	if err := exec.Command("git", "-C", dir, "checkout", first).Run(); err == nil {
		t.Fatal("baseline broken: checkout succeeded on a dirty tree")
	}

	if out, err := prepareBuildSourceForCheckout(context.Background(), dir); err != nil {
		t.Fatalf("prepare failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "checkout", first).CombinedOutput(); err != nil {
		t.Fatalf("checkout still blocked after prepare: %v\n%s", err, out)
	}
}

// The 62 phantom modifications. core.fileMode=false must make them invisible,
// which is the non-destructive answer — no command has to "clean" anything.
func TestPrepareBuildSource_SilencesModeOnlyChanges(t *testing.T) {
	dir, _ := newSourceRepo(t)

	if err := os.Chmod(filepath.Join(dir, "next.config.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if status := git(t, dir, "status", "--porcelain"); !strings.Contains(status, "next.config.js") {
		t.Skipf("filesystem does not report exec bits to git; nothing to silence (status=%q)", status)
	}

	if out, err := prepareBuildSourceForCheckout(context.Background(), dir); err != nil {
		t.Fatalf("prepare failed: %v\n%s", err, out)
	}

	if got := strings.TrimSpace(git(t, dir, "config", "core.fileMode")); got != "false" {
		t.Errorf("core.fileMode = %q, want \"false\"", got)
	}
	// And the file keeps its exec bit — nothing was rewritten to achieve this.
	info, err := os.Stat(filepath.Join(dir, "next.config.js"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("prepare stripped the exec bit instead of telling git to ignore it")
	}
	if status := strings.TrimSpace(git(t, dir, "status", "--porcelain")); status != "" {
		t.Errorf("mode noise still reported after prepare: %q", status)
	}
}

// The rule with teeth. `git clean` is banned outright, so untracked files
// survive even in a tree we are allowed to reset. This is the property that
// protects CLAUDE.md, FUTURE_WORK.md and docs/superpowers/ if this code is ever
// pointed somewhere it should not be.
func TestPrepareBuildSource_NeverRemovesUntrackedFiles(t *testing.T) {
	dir, _ := newSourceRepo(t)

	untracked := []string{"CLAUDE.md", "FUTURE_WORK.md", "docs/superpowers/skill.md", ".superpowers/state.json"}
	for _, name := range untracked {
		write(t, dir, name, "irreplaceable\n")
	}
	// Also dirty a tracked file, so the reset actually has work to do.
	write(t, dir, "pnpm-lock.yaml", "lockfileVersion: 9\nchanged: true\n")

	if out, err := prepareBuildSourceForCheckout(context.Background(), dir); err != nil {
		t.Fatalf("prepare failed: %v\n%s", err, out)
	}

	for _, name := range untracked {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("untracked file %s was destroyed: %v", name, err)
			continue
		}
		if string(body) != "irreplaceable\n" {
			t.Errorf("untracked file %s was modified: %q", name, body)
		}
	}
}

// Tracked content is what the reset is for: the local basePath edit and the
// regenerated lockfile go away, because the only correct content of a build
// source tree is whatever the requested commit says.
func TestPrepareBuildSource_DiscardsTrackedEdits(t *testing.T) {
	dir, _ := newSourceRepo(t)
	want := git(t, dir, "show", "HEAD:next.config.js")

	write(t, dir, "next.config.js", "const nextConfig = {\n  basePath: '/ckstats',\n}\n")
	if out, err := prepareBuildSourceForCheckout(context.Background(), dir); err != nil {
		t.Fatalf("prepare failed: %v\n%s", err, out)
	}

	got, err := os.ReadFile(filepath.Join(dir, "next.config.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("tracked edit survived the reset:\ngot  %q\nwant %q", got, want)
	}
}

// --- the allowlist that keeps this away from /repo ---

// /repo is the user's own project checkout, bind-mounted from
// /home/truffel/Project-Truffels. Resetting it would discard uncommitted work.
// This assertion is the whole safety argument, so it is asserted directly
// rather than inferred.
func TestResettableRepoDirs_ExcludesTheUsersRepo(t *testing.T) {
	if isResettableRepoDir("/repo") {
		t.Fatal("/repo is resettable; a checkout would discard the user's uncommitted work")
	}
	if !isAllowedRepoDir("/repo") {
		t.Fatal("baseline broken: /repo should still be a valid checkout target")
	}
}

// Only build source trees may be reset, and the list must stay explicit —
// adding an entry has to be a deliberate act, not a side effect of extending
// allowedRepoDirs.
func TestResettableRepoDirs_OnlyBuildSources(t *testing.T) {
	want := map[string]bool{"/srv/truffels/data/ckpoolstats": true}
	for dir := range resettableRepoDirs {
		if !want[dir] {
			t.Errorf("unexpected resettable dir %q: adding one destroys tracked local\n"+
				"changes there on every update; confirm it is a throwaway build source", dir)
		}
		if !isAllowedRepoDir(dir) {
			t.Errorf("resettable dir %q is not an allowed repo dir; the lists have drifted", dir)
		}
	}
	for dir := range want {
		if !isResettableRepoDir(dir) {
			t.Errorf("build source %q is not resettable; ckstats updates stay blocked", dir)
		}
	}
}

// No `git clean` may reach the source, in any form. Grepping the source is
// crude, but this is a rule about what the program is allowed to contain, and
// a behavioural test cannot prove the absence of a call on an untested path.
func TestAgentSource_ContainsNoGitClean(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Skipf("source not readable: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if strings.Contains(line, `"clean"`) && strings.Contains(line, "git") {
			t.Errorf("git clean found in agent source; untracked files must never be\n"+
				"removed, in any directory: %s", strings.TrimSpace(line))
		}
	}
}
