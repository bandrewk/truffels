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

// registerResettable makes a temp repo eligible for prepareBuildSourceForCheckout
// for the duration of one test.
//
// The behaviour tests below cannot use the real path — that is the user's live
// ckstats tree, and running `reset --hard` against it is precisely the damage
// this code guards. So the temp dir is added to the allowlist and removed again
// on cleanup. The production list is unchanged outside the test, which
// TestResettableRepoDirs_OnlyBuildSources verifies independently.
func registerResettable(t *testing.T, dir string) {
	t.Helper()
	resettableRepoDirs[dir] = true
	t.Cleanup(func() { delete(resettableRepoDirs, dir) })
}

// newSourceRepo builds a two-commit repo standing in for the ckstats upstream
// checkout, and returns the repo path plus the first commit's hash.
func newSourceRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	registerResettable(t, dir)
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

// --- removeUntrackedCollisionsForCheckout ---
//
// The second half of the same outage. With the tree reset, `git checkout` still
// aborted:
//
//	error: The following untracked working tree files would be overwritten by checkout:
//		pnpm-workspace.yaml
//
// The target commit tracks pnpm-workspace.yaml, the current HEAD does not, and
// a local `pnpm install` had left an untracked copy in the way. reset --hard
// cannot help — the file is untracked, so it is not the reset's business — and
// the update was stuck with no way out.

// newCollisionRepo builds a repo where the target commit tracks a file that
// HEAD does not, standing in for the ckstats tree at the moment it jammed.
// Returns the repo dir and the target commit.
//
// t.TempDir() is resolved through EvalSymlinks first: the removal path runs
// every candidate through validateUnderRoot, which rejects a root reached via a
// symlinked ancestor. On a host where /tmp is a symlink an unresolved temp dir
// would fail the check for reasons that have nothing to do with the behaviour
// under test.
func newCollisionRepo(t *testing.T) (string, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registerResettable(t, dir)
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.invalid")
	git(t, dir, "config", "user.name", "t")

	// Commit A: no pnpm-workspace.yaml.
	write(t, dir, "next.config.js", "const nextConfig = {}\n")
	git(t, dir, "add", "next.config.js")
	git(t, dir, "commit", "-q", "-m", "A")
	first := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	// Commit B: pnpm-workspace.yaml is tracked from here on.
	write(t, dir, "pnpm-workspace.yaml", "packages:\n  - upstream\n")
	git(t, dir, "add", "pnpm-workspace.yaml")
	git(t, dir, "commit", "-q", "-m", "B")
	target := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

	// Back to A, so the file is absent from the index and from HEAD.
	git(t, dir, "checkout", "-q", first)
	return dir, target
}

// The bug: an untracked file that the target commit tracks blocks the checkout
// permanently. It must be removed — and only it.
func TestRemoveUntrackedCollisions_UnblocksCheckout(t *testing.T) {
	dir, target := newCollisionRepo(t)

	// The leftover from a local `pnpm install`, and a second untracked entry
	// that the target does NOT track and therefore must survive untouched.
	write(t, dir, "pnpm-workspace.yaml", "packages:\n  - local-junk\n")
	write(t, dir, ".pnpm-store/v3/files/00/abc", "cached tarball\n")

	// Baseline: without the fix the checkout fails, or this test proves nothing.
	out, err := exec.Command("git", "-C", dir, "checkout", target).CombinedOutput()
	if err == nil {
		t.Fatal("baseline broken: checkout succeeded despite the untracked collision")
	}
	if !strings.Contains(string(out), "untracked working tree files") {
		t.Fatalf("baseline broken: checkout failed for the wrong reason:\n%s", out)
	}

	if log, err := removeUntrackedCollisionsForCheckout(context.Background(), dir, target); err != nil {
		t.Fatalf("remove failed: %v\n%s", err, log)
	} else if !strings.Contains(log, "pnpm-workspace.yaml") {
		t.Errorf("removal was not logged with its path: %q", log)
	}

	if out, err := exec.Command("git", "-C", dir, "checkout", target).CombinedOutput(); err != nil {
		t.Fatalf("checkout still blocked: %v\n%s", err, out)
	}

	// The collision now carries the target commit's content, not the local one.
	body, err := os.ReadFile(filepath.Join(dir, "pnpm-workspace.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "packages:\n  - upstream\n" {
		t.Errorf("pnpm-workspace.yaml = %q, want the target commit's content", body)
	}

	// The untracked file the target does not track is untouched. This is the
	// difference between the intersection and `git clean`.
	body, err = os.ReadFile(filepath.Join(dir, ".pnpm-store/v3/files/00/abc"))
	if err != nil {
		t.Fatalf("untracked non-colliding file was destroyed: %v", err)
	}
	if string(body) != "cached tarball\n" {
		t.Errorf("untracked non-colliding file was modified: %q", body)
	}
}

// Ignored files are excluded from the candidate list on purpose:
// --exclude-standard matches what git itself does, and git overwrites ignored
// files during checkout without complaining. Listing them would widen the blast
// radius for no benefit.
func TestRemoveUntrackedCollisions_LeavesIgnoredFilesToGit(t *testing.T) {
	dir, target := newCollisionRepo(t)
	write(t, dir, ".gitignore", "*.log\n")
	write(t, dir, "build.log", "local build output\n")
	write(t, dir, "pnpm-workspace.yaml", "packages:\n  - local-junk\n")

	if log, err := removeUntrackedCollisionsForCheckout(context.Background(), dir, target); err != nil {
		t.Fatalf("remove failed: %v\n%s", err, log)
	}
	if _, err := os.Stat(filepath.Join(dir, "build.log")); err != nil {
		t.Errorf("ignored file was removed: %v", err)
	}
}

// Same guard, same reasoning as prepareBuildSourceForCheckout: this function
// deletes files, so it must refuse anything outside the build-source allowlist
// *before* executing git, not merely because its one caller is wired correctly.
// The shim records every invocation; the canary must never appear.
func TestRemoveUntrackedCollisions_RefusesNonBuildSourceWithoutRunningGit(t *testing.T) {
	shimDir := t.TempDir()
	canary := filepath.Join(shimDir, "git-was-invoked")
	shim := "#!/bin/sh\necho \"$@\" >> " + canary + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir)

	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Fatalf("shim not reachable on PATH: %v", err)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("control failed: shim ran but left no canary: %v", err)
	}
	if err := os.Remove(canary); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{"/repo", "/", "/srv/truffels/data", "/home/truffel/Project-Truffels", ""} {
		out, err := removeUntrackedCollisionsForCheckout(context.Background(), dir, "8f2e7c2f8403")
		if err == nil {
			t.Errorf("remove accepted %q; it may only ever touch build source trees", dir)
		}
		if out != "" {
			t.Errorf("remove returned output for %q, so it did work before refusing: %q", dir, out)
		}
		if err != nil && !strings.Contains(err.Error(), dir) {
			t.Errorf("error for %q does not name the directory: %v", dir, err)
		}
		if _, statErr := os.Stat(canary); statErr == nil {
			body, _ := os.ReadFile(canary)
			t.Fatalf("remove executed git for %q before refusing: %s", dir, body)
		}
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

// The guard has to live inside prepareBuildSourceForCheckout, not only at its
// call site in handleGitCheckout. The call site is correct today, but a
// function that discards tracked changes must not be safe merely because its
// single caller happens to be wired right — the cost of a future caller getting
// it wrong is the user's uncommitted work in /repo.
//
// Proving "it returned an error" is not enough: it must refuse *before*
// executing anything. So PATH is pointed at a fake `git` that records having
// been invoked, and the test asserts that record never appears. A real git can
// never run here, which is deliberate — the resettable path is the user's live
// ckstats tree and an accidental `reset --hard` against it would be exactly the
// damage this guard exists to prevent.
func TestPrepareBuildSource_RefusesNonBuildSourceWithoutRunningGit(t *testing.T) {
	shimDir := t.TempDir()
	canary := filepath.Join(shimDir, "git-was-invoked")
	shim := "#!/bin/sh\necho \"$@\" >> " + canary + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir)

	// Control: the shim really is what `git` resolves to, so a missing canary
	// below means "not executed", not "shim broken".
	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Fatalf("shim not reachable on PATH: %v", err)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("control failed: shim ran but left no canary: %v", err)
	}
	if err := os.Remove(canary); err != nil {
		t.Fatal(err)
	}

	// /repo is the user's own checkout. Anything not on the resettable list
	// must be refused identically.
	for _, dir := range []string{"/repo", "/", "/srv/truffels/data", "/home/truffel/Project-Truffels", ""} {
		out, err := prepareBuildSourceForCheckout(context.Background(), dir)
		if err == nil {
			t.Errorf("prepare accepted %q; it may only ever touch build source trees", dir)
		}
		if out != "" {
			t.Errorf("prepare returned output for %q, so it did work before refusing: %q", dir, out)
		}
		if err != nil && !strings.Contains(err.Error(), dir) {
			t.Errorf("error for %q does not name the directory: %v", dir, err)
		}
		if _, statErr := os.Stat(canary); statErr == nil {
			body, _ := os.ReadFile(canary)
			t.Fatalf("prepare executed git for %q before refusing: %s", dir, body)
		}
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
