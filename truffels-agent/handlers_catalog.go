package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
)

// loadedCatalog is filled once at startup. The catalog lives only here in the
// agent; the API fetches it via this endpoint. This makes a version skew between
// the two constructively impossible.
var loadedCatalog Catalog

func handleCatalogGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, loadedCatalog)
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
		{filepath.Dir(catConfigPath(req.ID, "x")), 0o755},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir: " + err.Error()})
			return
		}
	}
	for name, content := range configs {
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

	if err := os.RemoveAll(catComposeDir(req.ID)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "compose-dir: " + err.Error()})
		return
	}

	// Rendered config files are artifacts derived from the catalog, not user
	// data: they are removed together with the compose dir. The data dir is
	// only ever touched when the caller explicitly asks via purge_data.
	if err := os.RemoveAll(filepath.Dir(catConfigPath(req.ID, "x"))); err != nil {
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
