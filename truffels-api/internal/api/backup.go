package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"truffels-api/internal/model"
)

const (
	backupDir    = "/srv/truffels/backups"
	maxBackups   = 5
)

func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	_ = os.MkdirAll(backupDir, 0750)

	ts := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("truffels-backup-%s.tar.gz", ts)
	outPath := filepath.Join(backupDir, filename)

	// Paths to include (relative to /srv/truffels)
	includes := []string{
		"config",
		"compose",
		"data/truffels/truffels.db",
	}

	// Check if secrets requested
	if r.URL.Query().Get("include_secrets") == "true" {
		includes = append(includes, "secrets")
	}

	args := []string{
		"czf", outPath,
		"-C", "/srv/truffels",
	}
	args = append(args, includes...)

	cmd := exec.Command("tar", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Error("backup failed", "err", err, "output", string(out))
		_ = s.store.UpsertAlert(&model.Alert{
			Type:     "backup_failed",
			Severity: model.SeverityWarning,
			Message:  "Backup export failed: " + err.Error(),
		})
		writeError(w, http.StatusInternalServerError, "backup failed: "+err.Error())
		return
	}

	// Prune old backups
	pruneBackups()

	// Resolve any previous backup_failed alerts on success
	_ = s.store.ResolveAlerts("backup_failed", "")

	_ = s.store.LogAudit("backup_export", filename, "", r.RemoteAddr)

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ok",
		"filename": filename,
		"path":     outPath,
	})
}

func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}

	var backups []map[string]interface{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, _ := e.Info()
		backups = append(backups, map[string]interface{}{
			"filename": e.Name(),
			"size_mb":  float64(info.Size()) / (1024 * 1024),
			"created":  info.ModTime().Format(time.RFC3339),
		})
	}

	if backups == nil {
		backups = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, backups)
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" || strings.Contains(filename, "/") || strings.Contains(filename, "..") {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	path := filepath.Join(backupDir, filename)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	http.ServeFile(w, r, path)
}

func pruneBackups() {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}

	var tarballs []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tar.gz") {
			tarballs = append(tarballs, e)
		}
	}

	if len(tarballs) <= maxBackups {
		return
	}

	// Sort by name (timestamp-based, oldest first)
	sort.Slice(tarballs, func(i, j int) bool {
		return tarballs[i].Name() < tarballs[j].Name()
	})

	for i := 0; i < len(tarballs)-maxBackups; i++ {
		_ = os.Remove(filepath.Join(backupDir, tarballs[i].Name()))
	}
}
