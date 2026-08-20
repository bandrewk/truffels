package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
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
