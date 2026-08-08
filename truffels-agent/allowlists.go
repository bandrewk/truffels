package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This file is the trust boundary of truffels-agent.
//
// The agent runs as root with the Docker socket bind-mounted, on an appliance
// that holds a Bitcoin full node, its wallet-adjacent state and the user's own
// project checkout. Every declaration below decides what that root process will
// act on: which services and containers it will touch, which images it will
// pull or delete, which directories git may reset, which paths a request may
// name. Nothing outside these lists is reachable.
//
// They live together so the whole boundary can be read in one pass, and so a
// change to it is visible as a change to this file. Treat any edit here as a
// security change: widening a list, relaxing a character check or replacing an
// exact match with a prefix or a cleaned path is a grant of privilege, not a
// refactor. Review it as such.

// --- Services and containers ---

// Allowlisted service IDs and their compose directory names.
var allowedServices = map[string]string{
	"bitcoind":       "bitcoin",
	"electrs":        "electrs",
	"ckpool":         "ckpool",
	"mempool":        "mempool",
	"ckstats":        "ckstats",
	"proxy":          "proxy",
	"mempool-db":     "mempool",
	"ckstats-db":     "ckstats",
	"truffels":       "truffels",
	"truffels-agent": "truffels",
	"truffels-api":   "truffels",
	"truffels-web":   "truffels",
}

// Allowlisted container names for inspection.
var allowedContainers = map[string]bool{
	"truffels-bitcoind":         true,
	"truffels-electrs":          true,
	"truffels-ckpool":           true,
	"truffels-mempool-backend":  true,
	"truffels-mempool-frontend": true,
	"truffels-mempool-db":       true,
	"truffels-ckstats":          true,
	"truffels-ckstats-cron":     true,
	"truffels-ckstats-db":       true,
	"truffels-proxy":            true,
	"truffels-agent":            true,
	"truffels-api":              true,
	"truffels-web":              true,
}

// --- Container images ---

// allowedImagePrefixes names the image namespaces this appliance manages: the
// images it builds itself plus the upstream images of the services it runs.
// btcpayserver/ is Bitcoin Core and getumbrel/ is electrs — both are live, and
// neither is a leftover.
var allowedImagePrefixes = []string{
	"truffels/", "mempool/", "btcpayserver/", "getumbrel/",
	"caddy:", "postgres:", "mariadb:",
}

// Image references reach docker as a single argv element — exec.Command takes
// an argv and no path here goes through a shell — so a metacharacter cannot
// start a second command today. The charset check is what keeps that true if
// the execution path ever changes, and it is the reasoning isAllowedImageRef
// has carried since it was written. The same reasoning now covers the same kind
// of input on the other two endpoints.
//
// Two alphabets, not one, and the difference is load-bearing:
//
//   - managedImageRefChars covers upstream references. Every upstream image in
//     this project is digest-pinned — install.sh writes
//     "mariadb:lts@sha256:8164f18…" — so '@' and hex must pass or every
//     pull-based service stops updating.
//   - localImageRefChars covers the images this appliance builds itself. Those
//     are only ever "truffels/<repo>:<tag>"; nothing in truffels-api ever
//     constructs a digest for one (composeImageRe does not even match a ref
//     carrying '@'). Handing them the upstream alphabet would widen
//     isAllowedImageRef with no caller asking for it.
//
// So the loop is shared and the alphabet is not. A single merged set could only
// be the union, and the union is the looser of the two.
//
// Charset only — deliberately not a reference parser. A parser normalises, and
// this file's whole premise is that nothing here normalises before deciding.
// It would also reject shapes that are real: pruneOldImages builds
// "<image>:<version>", and for the digest-tracked mempool-db that yields
// "mariadb:sha256:b1c7…" — two colons, no '@', not a well-formed reference, and
// today answered with a best-effort 200 rather than a 403.
const (
	localImageRefChars   = "abcdefghijklmnopqrstuvwxyz0123456789/:._-"
	managedImageRefChars = localImageRefChars + "@"
)

func hasOnlyChars(s, allowed string) bool {
	for _, c := range s {
		if !strings.ContainsRune(allowed, c) {
			return false
		}
	}
	return true
}

// isAllowedManagedImage gates both /v1/image/pull and /v1/image/remove.
//
// One predicate for both on purpose. They take the same kind of input — a
// reference to an image on this appliance — and hand it to the same root-level
// docker CLI; the only difference is the verb. An image this box may not delete
// is an image it has no reason to fetch, and a `docker pull` of an attacker's
// choosing on a machine holding a Bitcoin node is a worse outcome than a stray
// `docker rmi`, not a milder one. Splitting the two checks is how /v1/image/pull
// came to have none at all.
//
// Distinct from isAllowedImageRef below: that one covers the *locally built*
// images only, and is stricter for reasons documented there.
func isAllowedManagedImage(ref string) bool {
	if !hasOnlyChars(ref, managedImageRefChars) {
		return false
	}
	for _, prefix := range allowedImagePrefixes {
		if strings.HasPrefix(ref, prefix) {
			return true
		}
	}
	return false
}

// allowedLocalImageRepos are the image repositories this appliance builds
// itself. Five, and they are enumerated rather than matched by the "truffels/"
// prefix, because the prefix is a namespace an attacker may name freely: with
// it alone, /v1/image/tag would happily move any tag onto truffels/anything and
// /v1/image/inspect-by-name would report on it.
//
// NOT derivable from allowedServices, and it must not be generated from it. The
// service is "truffels-agent"; the image it runs is "truffels/agent". The
// service "ckstats" runs both the truffels-ckstats and truffels-ckstats-cron
// containers off the single truffels/ckstats image, so there is no
// truffels/ckstats-cron. And the "truffels" service has no image of its own at
// all — it is the stack of the three below.
//
// The five: agent, api and web are the self-update targets, built and tagged
// truffels/<name>:<release>. ckpool and ckstats are the NeedsBuild services,
// built from vendored sources and tagged with a version, a commit hash, :latest
// or :rollback. Verified against `docker images` on the device.
//
// Adding one means a sixth image exists. Check that before adding it.
var allowedLocalImageRepos = map[string]bool{
	"truffels/agent":   true,
	"truffels/api":     true,
	"truffels/web":     true,
	"truffels/ckpool":  true,
	"truffels/ckstats": true,
}

// isAllowedImageRef restricts image operations to images this appliance builds
// itself. Anything else — upstream images, an unknown repository in our own
// namespace, anything with shell metacharacters — is refused; the agent runs as
// root against the docker socket. Exact charset check and an exact repository
// match, no normalising: a cleaned or lowercased ref would let something that
// should fail slip through.
//
// The tag is split off at the last ':' and then deliberately not inspected
// beyond the charset. Tags legitimately take four unrelated shapes here
// (:v0.3.1-dev.29, :v1.0.0, :latest, :rollback, and a bare commit hash for
// ckstats), and a pattern covering all of them would say nothing a charset
// check does not already say. A ref with no tag keeps passing, exactly as
// before: the repository is what this decides on.
func isAllowedImageRef(ref string) bool {
	if !hasOnlyChars(ref, localImageRefChars) {
		return false
	}
	repo := ref
	if idx := strings.LastIndex(repo, ":"); idx >= 0 {
		repo = repo[:idx]
	}
	return allowedLocalImageRepos[repo]
}

// --- Journal queries ---

var allowedPriorities = map[string]bool{
	"": true, "emerg": true, "crit": true, "err": true,
	"warning": true, "info": true, "debug": true,
}

var allowedUnits = map[string]bool{
	"": true, "docker": true, "kernel": true, "systemd": true,
	"nftables": true, "ssh": true,
}

// --- Git repository directories ---

// allowedRepoDirs are the only directories git operations may touch. Exact
// string match only — no prefix matching, no path cleaning. A cleaned path
// would let "/repo/../etc" normalise into something that passes.
var allowedRepoDirs = map[string]bool{
	"/repo":                          true,
	"/srv/truffels/data/ckpoolstats": true,
}

func isAllowedRepoDir(dir string) bool {
	return allowedRepoDirs[dir]
}

// resettableRepoDirs is deliberately a SECOND, strictly narrower list rather
// than a flag on allowedRepoDirs or a field on the request.
//
// "/repo" is the user's own project checkout, bind-mounted from
// /home/truffel/Project-Truffels. It holds untracked files they intend to keep
// — CLAUDE.md, FUTURE_WORK.md, docs/superpowers/, .superpowers/ — and, during
// development, uncommitted work in tracked files. Discarding anything there
// destroys work that no update can recreate. It must never appear in this map.
//
// Only build source trees belong here: throwaway upstream checkouts whose sole
// purpose is to be fed to `docker build`, where the only correct content is
// whatever the requested commit says. Nothing in them is authored locally, so
// nothing in them is worth preserving.
//
// The decision is made here, server-side, on the path itself. A request field
// would put it in the caller's hands, and a caller that sends the wrong value
// once deletes the user's files permanently.
var resettableRepoDirs = map[string]bool{
	"/srv/truffels/data/ckpoolstats": true,
}

func isResettableRepoDir(dir string) bool {
	return resettableRepoDirs[dir]
}

// --- Git references ---

func isValidTag(tag string) bool {
	if len(tag) < 2 || tag[0] != 'v' {
		return false
	}
	for _, c := range tag[1:] {
		if c != '.' && c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

// isValidCommitHash accepts abbreviated and full git object names: lowercase
// hex, 7 to 40 characters. Deliberately separate from isValidTag so loosening
// one cannot weaken the other.
func isValidCommitHash(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// --- Filesystem paths ---

// validateUnderRoot returns the cleaned absolute path if it lies under `root`,
// is not a symlink redirecting outside `root`, and contains no relative-path
// escapes. Returns ("", error) otherwise. The path's parent must exist for
// EvalSymlinks to resolve cleanly; if the parent doesn't exist yet, the check
// walks upward until it finds an existing ancestor and validates that one.
//
// KNOWN LIMIT — this is not a defence against a racing attacker.
//
// The symlink check resolves the nearest ancestor that exists *at the time of
// the call*. Every component created afterwards is unexamined, and so is any
// component swapped for a symlink between this returning and the caller's
// os.MkdirAll / os.WriteFile / os.RemoveAll running. That is a plain
// time-of-check/time-of-use gap and it cannot be closed from here: closing it
// means the file operations themselves must not traverse a symlink — openat2
// with RESOLVE_BENEATH, or an fd-anchored walk — which is a rewrite of every
// caller, not a change to this function.
//
// It is stated rather than papered over because the callers' safety arguments
// have to be able to lean on what this actually promises. What it does promise:
// no relative escape, and no symlink escape via a component that already
// existed. What it does not: atomicity with the operation that follows. The
// exposure is bounded elsewhere — /srv/truffels is root-owned, the endpoints
// take service-shaped inputs, and clear-dir carries its own basename and depth
// checks — not by this.
func validateUnderRoot(p, root string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	// Normalise and check the root before using it as a prefix. It is a
	// parameter like any other, and nothing verified it: a root with a trailing
	// slash made root+"/" a double slash that no cleaned path can match, so
	// every request under it was silently refused, and an empty root made the
	// prefix "/", which matches every absolute path there is — the boundary
	// inverted, in both directions, from a caller's typo. "/" is refused for the
	// same reason as "": a root containing the whole filesystem bounds nothing.
	//
	// Today's callers pass clean absolute values (composeRoot, configRoot,
	// dataRoot and repoDir), so this changes no live behaviour. That was luck,
	// and luck is not a check.
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || root == "/" {
		return "", fmt.Errorf("invalid root %q", root)
	}
	cleaned := filepath.Clean(p)
	if !strings.HasPrefix(cleaned, root+"/") {
		return "", fmt.Errorf("path outside root")
	}
	// Walk up to the nearest existing ancestor and EvalSymlinks it. This catches
	// symlink redirects (e.g. /srv/truffels/data/x is a symlink to /etc).
	probe := cleaned
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe || parent == "/" {
			break
		}
		probe = parent
	}
	resolved, err := filepath.EvalSymlinks(probe)
	if err == nil && resolved != probe {
		// A symlink was traversed somewhere up the chain. Verify the target
		// still lies under root; reject if it escaped.
		if !strings.HasPrefix(resolved+"/", root+"/") && resolved != root {
			return "", fmt.Errorf("symlink redirects outside root")
		}
	}
	return cleaned, nil
}
