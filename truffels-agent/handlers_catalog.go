package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// loadedCatalog is filled once at startup. The catalog lives only here in the
// agent; the API fetches it via this endpoint. This makes a version skew between
// the two constructively impossible.
var loadedCatalog Catalog

// catalogEntryResponse wraps CatalogEntry with the derived container names.
// Container name derivation is the agent's responsibility; the API consumes
// what /v1/catalog reports. One rule, one place.
type catalogEntryResponse struct {
	CatalogEntry
	ContainerNames []string `json:"container_names"`
}

func handleCatalogGet(w http.ResponseWriter, r *http.Request) {
	// Convert each CatalogEntry to catalogEntryResponse, computing container_names.
	response := make(map[string]catalogEntryResponse)
	for id, entry := range loadedCatalog {
		names := make([]string, 0, len(entry.Containers))
		for _, container := range entry.Containers {
			names = append(names, catContainerName(id, container.Name))
		}
		response[id] = catalogEntryResponse{
			CatalogEntry:   entry,
			ContainerNames: names,
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func handleServiceApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string         `json:"id"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	// 1. Charset. No cleaning, no normalizing.
	if !isValidCatalogID(req.ID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid id"})
		return
	}
	// 2. Must be in the embedded catalog.
	entry, ok := loadedCatalog[req.ID]
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "unknown catalog entry"})
		return
	}
	// 3. Parameters against the declared schema.
	params, err := ValidateParams(entry, req.Params)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// 4. Render — still without any write access.
	composeOut, err := RenderCompose(entry, params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	configs, err := RenderConfig(req.ID, params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// From here on we write. Everything before could only reject.
	dirs := []struct {
		path string
		mode os.FileMode
	}{
		{catComposeDir(req.ID), 0o755},
		{catDataDir(req.ID), 0o755},
		{catConfigDir(req.ID), 0o755},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir: " + err.Error()})
			return
		}
	}
	// The data dir must be writable by the (typically non-root) user the
	// container runs as. The agent creates it as root, so hand ownership over.
	// Config dirs stay root-owned on purpose: config files are mounted read-only.
	if uid, gid, ok := dataDirOwner(entry); ok {
		if err := os.Chown(catDataDir(req.ID), uid, gid); err != nil {
			// The agent runs as root in production, where chown always
			// succeeds. In a non-root test/dev environment chown to a foreign
			// uid is EPERM and there is nothing to hand over, so continue.
			if os.Geteuid() == 0 {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "chown data dir: " + err.Error()})
				return
			}
			slog.Warn("data dir chown skipped (agent not running as root)", "id", req.ID, "err", err)
		}
	}
	for name, content := range configs {
		if err := safeConfigKey(name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config key: " + err.Error()})
			return
		}
		if err := os.WriteFile(catConfigPath(req.ID, name), content, 0o644); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config: " + err.Error()})
			return
		}
	}
	if err := os.WriteFile(catComposeDir(req.ID)+"/docker-compose.yml", composeOut, 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "compose: " + err.Error()})
		return
	}

	slog.Info("Catalog service applied", "id", req.ID)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": req.ID})
}

func handleServiceRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string `json:"id"`
		PurgeData bool   `json:"purge_data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if !isValidCatalogID(req.ID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid id"})
		return
	}
	if _, ok := loadedCatalog[req.ID]; !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "unknown catalog entry"})
		return
	}

	// Spec section 10: compose down BEFORE files disappear. Otherwise a
	// running catalog container would be orphaned the moment its compose
	// file is deleted. Only attempted when a compose file actually exists —
	// a service that was applied but never started must still remove cleanly
	// on hosts where docker is unavailable to the test environment.
	composeFile := catComposeDir(req.ID) + "/docker-compose.yml"
	if _, err := os.Stat(composeFile); err == nil {
		if err := runCompose(catComposeDir(req.ID), "down"); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "compose down: " + err.Error()})
			return
		}
	}

	if err := os.RemoveAll(catComposeDir(req.ID)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "compose-dir: " + err.Error()})
		return
	}

	// Rendered config files are artifacts derived from the catalog, not user
	// data: they are removed together with the compose dir. The data dir is
	// only ever touched when the caller explicitly asks via purge_data.
	if err := os.RemoveAll(catConfigDir(req.ID)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config-dir: " + err.Error()})
		return
	}

	// Data remains in place unless explicitly requested otherwise.
	if req.PurgeData {
		if err := os.RemoveAll(catDataDir(req.ID)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "data-dir: " + err.Error()})
			return
		}
		slog.Warn("Catalog data deleted", "id", req.ID, "path", catDataDir(req.ID))
	}

	slog.Info("Catalog service removed", "id", req.ID, "purge_data", req.PurgeData)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "purged": req.PurgeData})
}

// handleServiceChainProbe runs a catalog chain node's sync probe inside its
// container and returns the probe's stdout verbatim (bitcoin-core style JSON,
// e.g. getblockchaininfo). The command is taken from the embedded catalog, not
// the request, so the endpoint can only ever run the fixed, curated probe for a
// known entry — never an arbitrary command.
func handleServiceChainProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if !isValidCatalogID(req.ID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid id"})
		return
	}
	entry, ok := loadedCatalog[req.ID]
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "unknown catalog entry"})
		return
	}
	if entry.ChainInfo == nil || len(entry.ChainInfo.SyncProbe) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "entry has no sync probe"})
		return
	}
	if len(entry.Containers) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "entry has no container"})
		return
	}
	// The probe runs in the entry's primary (first) container — the node itself.
	container := catContainerName(req.ID, entry.Containers[0].Name)
	if !isAllowedContainer(container) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "container not allowed"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// argv is the embedded probe, never caller input.
	args := append([]string{"exec", container}, entry.ChainInfo.SyncProbe...)
	out, err := runStdout(ctx, "docker", args...)
	if err != nil {
		// A stopped or still-starting node cannot answer; the caller treats this
		// as "no sync info yet" rather than an error condition.
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "probe failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

// dataDirOwner returns the uid/gid that should own the catalog data directory:
// the numeric user of the container that mounts the data volume. Catalog
// containers drop all capabilities and run unprivileged, so a root-owned data
// dir would leave them unable to write. Returns ok=false when no container
// mounts a data volume or its user is not a numeric uid[:gid].
func dataDirOwner(e CatalogEntry) (uid, gid int, ok bool) {
	for _, c := range e.Containers {
		mountsData := false
		for _, v := range c.Volumes {
			if v.Kind == "data" {
				mountsData = true
				break
			}
		}
		if mountsData {
			return parseNumericUser(c.User)
		}
	}
	return 0, 0, false
}

// parseNumericUser parses a Docker "uid[:gid]" string of numeric ids. A bare
// "uid" uses that id for the group too, matching how the container resolves it.
// Empty or non-numeric users yield ok=false rather than an arbitrary owner.
func parseNumericUser(s string) (uid, gid int, ok bool) {
	if s == "" {
		return 0, 0, false
	}
	u, g, found := strings.Cut(s, ":")
	uid, err := strconv.Atoi(u)
	if err != nil {
		return 0, 0, false
	}
	gid = uid
	if found {
		gid, err = strconv.Atoi(g)
		if err != nil {
			return 0, 0, false
		}
	}
	return uid, gid, true
}
