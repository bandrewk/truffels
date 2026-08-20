package api

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"truffels-api/internal/model"
)

const (
	backupDir  = "/srv/truffels/backups"
	maxBackups = 5
)

// backupMu serializes backup exports. Backups share a fixed staging path;
// serializing exports keeps the pre-clean from deleting a concurrent run's snapshot.
var backupMu sync.Mutex

func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	backupMu.Lock()
	defer backupMu.Unlock()

	_ = os.MkdirAll(backupDir, 0750)

	// Instead of backing up data/truffels/truffels.db raw, create a consistent
	// snapshot first and back that up. Chain data stays intentionally out —
	// it is recoverable from the network and would bloat every backup.
	// Use staging at backupDir so the archived structure mirrors /srv/truffels layout.
	stagingRoot := filepath.Join(backupDir, "staging")
	stagingDataDir := filepath.Join(stagingRoot, "data", "truffels")

	// Clean up any stale staging from a previous failed backup
	if err := os.RemoveAll(stagingRoot); err != nil {
		slog.Warn("cleanup stale staging", "err", err)
	}

	// Create the directory structure: stagingRoot/data/truffels/
	if err := os.MkdirAll(stagingDataDir, 0o750); err != nil {
		slog.Error("backup staging", "err", err)
		writeError(w, http.StatusInternalServerError, "backup staging failed: "+err.Error())
		return
	}
	defer func() {
		if err := os.RemoveAll(stagingRoot); err != nil {
			slog.Warn("cleanup staging after backup", "err", err)
		}
	}()

	snapPath := filepath.Join(stagingDataDir, "truffels.db")
	if err := snapshotSQLite(s.store.DB(), snapPath); err != nil {
		slog.Error("sqlite snapshot", "err", err)
		writeError(w, http.StatusInternalServerError, "sqlite snapshot failed: "+err.Error())
		return
	}

	ts := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("truffels-backup-%s.tar.gz", ts)
	outPath := filepath.Join(backupDir, filename)

	// Paths to include (relative to /srv/truffels)
	includes := []string{
		"config",
		"compose",
	}

	// Check if secrets requested
	if r.URL.Query().Get("include_secrets") == "true" {
		includes = append(includes, "secrets")
	}

	// Build tar args: include config/compose/secrets, then add the snapshot
	// from staging with a second -C flag to place DB at data/truffels/truffels.db
	args := []string{
		"czf", outPath,
		"-C", "/srv/truffels",
	}
	args = append(args, includes...)
	// Second -C for staging to archive DB at its original path
	args = append(args, "-C", stagingRoot, "data/truffels/truffels.db")

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

// snapshotSQLite writes a self-contained consistent copy of the database.
//
// A raw file copy would not be consistent: when writes are in flight, part of
// the state lives in the WAL, and the copy could be created mid-transaction.
// VACUUM INTO handles this in the database engine.
//
// The destination file must not exist — SQLite itself requires this, and it is
// also the desired safeguard against accidentally overwriting an older backup.
func snapshotSQLite(db *sql.DB, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("destination file already exists: %s", dest)
	}
	if _, err := db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("VACUUM INTO %s: %w", dest, err)
	}
	return nil
}
