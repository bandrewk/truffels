package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The TOCTOU gap these tests cover cannot be reproduced by racing it — a test
// that plants a symlink in the microseconds between a check and a use is a test
// that fails on a busy machine and passes on an idle one, which is worse than no
// test.
//
// So they prove the property that makes the race irrelevant: the anchored
// operation refuses to leave the root *on its own*, with no help from
// validateUnderRoot. If that holds, it does not matter when the symlink appears,
// because the kernel resolves every component against the anchor at the moment
// of use. Each test therefore bypasses validateUnderRoot deliberately and hands
// anchorUnderRoot exactly what a winning attacker would have arranged.
//
// Each also carries a control doing the same thing unanchored, so the setup is
// shown to be a real trap rather than an inert one.

// escapeTrap builds a root containing a service directory that is really a
// symlink pointing outside it, with something worth destroying at the far end.
// Returns the root, the path a caller would name, and the outside file that must
// survive.
func escapeTrap(t *testing.T) (root, named, mustSurvive string) {
	t.Helper()
	base := t.TempDir()

	root = filepath.Join(base, "data")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}

	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "cache"), 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	mustSurvive = filepath.Join(outside, "cache", "keepme")
	if err := os.WriteFile(mustSurvive, []byte("not yours to delete"), 0o644); err != nil {
		t.Fatalf("write bait: %v", err)
	}

	// The escape sits in an intermediate component, which is the shape that
	// matters: os.RemoveAll on a final-component symlink unlinks the link
	// itself, so that case was never dangerous. "svc" being the link is.
	if err := os.Symlink(outside, filepath.Join(root, "svc")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	return root, filepath.Join(root, "svc", "cache"), mustSurvive
}

func TestAnchoredRemoveAll_RefusesToLeaveTheRoot(t *testing.T) {
	root, named, mustSurvive := escapeTrap(t)

	r, name, err := anchorUnderRoot(named, root)
	if err != nil {
		t.Fatalf("anchorUnderRoot: %v", err)
	}
	defer func() { _ = r.Close() }()

	if err := r.RemoveAll(name); err == nil {
		t.Error("RemoveAll followed a symlink out of the root and reported success")
	}
	if _, err := os.Stat(mustSurvive); err != nil {
		t.Fatalf("the file outside the root was destroyed: %v", err)
	}
}

// The control. Same trap, the operation this code used to perform. It proves the
// trap is real: without the anchor the outside file is gone.
func TestUnanchoredRemoveAll_DestroysTheTargetOutsideTheRoot(t *testing.T) {
	_, named, mustSurvive := escapeTrap(t)

	if err := os.RemoveAll(named); err != nil {
		t.Fatalf("control setup failed: %v", err)
	}
	if _, err := os.Stat(mustSurvive); err == nil {
		t.Fatal("control did not reproduce the escape — the trap is inert and " +
			"the anchored test above proves nothing")
	}
}

func TestAnchoredMkdirAllAndChmod_RefuseToLeaveTheRoot(t *testing.T) {
	root, named, _ := escapeTrap(t)

	r, name, err := anchorUnderRoot(named, root)
	if err != nil {
		t.Fatalf("anchorUnderRoot: %v", err)
	}
	defer func() { _ = r.Close() }()

	if err := r.MkdirAll(name, 0o755); err == nil {
		t.Error("MkdirAll created a directory outside the root")
	}
	if err := r.Chmod(name, 0o777); err == nil {
		t.Error("Chmod changed the mode of something outside the root")
	}
}

// A path that escapes by relative components never reaches os.Root at all.
func TestAnchorUnderRoot_RejectsRelativeEscapes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "data")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for _, p := range []string{
		filepath.Join(base, "elsewhere"),
		"/etc",
		base,
	} {
		if r, _, err := anchorUnderRoot(p, root); err == nil {
			_ = r.Close()
			t.Errorf("anchorUnderRoot(%q, %q) accepted a path outside the root", p, root)
		}
	}
}

// The everyday case has to keep working, or the guard is just an outage.
func TestAnchoredOperations_AcceptTheRealShapes(t *testing.T) {
	root := t.TempDir()
	named := filepath.Join(root, "mempool", "cache")

	r, name, err := anchorUnderRoot(named, root)
	if err != nil {
		t.Fatalf("anchorUnderRoot: %v", err)
	}
	defer func() { _ = r.Close() }()

	if name != filepath.Join("mempool", "cache") {
		t.Errorf("relative name = %q, want mempool/cache", name)
	}
	if err := r.MkdirAll(name, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := r.Chmod(name, 0o750); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if err := r.RemoveAll(name); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Stat(named); !os.IsNotExist(err) {
		t.Errorf("directory should be gone, stat gave %v", err)
	}
}
