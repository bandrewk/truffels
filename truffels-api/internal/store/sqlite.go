package store

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"truffels-api/internal/model"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	migrations := []string{
		"migrations/001_init.sql",
		"migrations/002_auth.sql",
		"migrations/003_updates.sql",
		"migrations/004_monitoring.sql",
		"migrations/005_fan_metrics.sql",
		"migrations/006_container_metrics.sql",
		"migrations/007_host_io_metrics.sql",
	}
	for _, m := range migrations {
		data, err := migrationsFS.ReadFile(m)
		if err != nil {
			return fmt.Errorf("read %s: %w", m, err)
		}
		// Execute each statement separately so ALTER TABLE "duplicate column"
		// errors don't prevent subsequent statements from running.
		for _, stmt := range splitStatements(string(data)) {
			if _, err := s.db.Exec(stmt); err != nil {
				// Tolerate "duplicate column" from ALTER TABLE on re-run
				if strings.Contains(err.Error(), "duplicate column") {
					continue
				}
				return fmt.Errorf("exec %s: %w", m, err)
			}
		}
	}
	return nil
}

// splitStatements splits SQL text into individual statements on semicolons,
// skipping empty and comment-only lines.
func splitStatements(sql string) []string {
	var stmts []string
	for _, s := range strings.Split(sql, ";") {
		s = strings.TrimSpace(s)
		// Skip empty and comment-only fragments
		if s == "" {
			continue
		}
		lines := strings.Split(s, "\n")
		allComments := true
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "--") {
				allComments = false
				break
			}
		}
		if allComments {
			continue
		}
		stmts = append(stmts, s)
	}
	return stmts
}

// GetSetting retrieves a value from admin_settings.
func (s *Store) GetSetting(key string) (string, error) {
	var val string
	err := s.db.QueryRow(`SELECT value FROM admin_settings WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// SetSetting upserts a value in admin_settings.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO admin_settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// LogAudit writes an entry to the audit log.
func (s *Store) LogAudit(action, target, detail, ip string) error {
	_, err := s.db.Exec(
		`INSERT INTO audit_log (action, target, detail, ip) VALUES (?, ?, ?, ?)`,
		action, target, detail, ip)
	return err
}

// GetAuditLog returns recent audit entries.
func (s *Store) GetAuditLog(limit int) ([]AuditEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, timestamp, action, target, detail, ip FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var target, detail, ip sql.NullString
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Action, &target, &detail, &ip); err != nil {
			continue
		}
		e.Target = target.String
		e.Detail = detail.String
		e.IP = ip.String
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

type AuditEntry struct {
	ID        int64  `json:"id"`
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Target    string `json:"target,omitempty"`
	Detail    string `json:"detail,omitempty"`
	IP        string `json:"ip,omitempty"`
}

// EnsureService creates a service record if it doesn't exist.
func (s *Store) EnsureService(id string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO services (id) VALUES (?)`, id)
	return err
}

// SetServiceEnabled updates the enabled flag.
func (s *Store) SetServiceEnabled(id string, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	_, err := s.db.Exec(
		`UPDATE services SET enabled = ?, updated_at = datetime('now') WHERE id = ?`,
		val, id)
	return err
}

// IsServiceEnabled checks if a service is enabled.
func (s *Store) IsServiceEnabled(id string) (bool, error) {
	var enabled int
	err := s.db.QueryRow(`SELECT enabled FROM services WHERE id = ?`, id).Scan(&enabled)
	if err == sql.ErrNoRows {
		return true, nil // default enabled
	}
	return enabled == 1, err
}

// CreateConfigRevision stores a new config revision.
func (s *Store) CreateConfigRevision(rev *model.ConfigRevision) error {
	_, err := s.db.Exec(
		`INSERT INTO config_revisions (service_id, actor, diff, config_snapshot, validation_result)
		 VALUES (?, ?, ?, ?, ?)`,
		rev.ServiceID, rev.Actor, rev.Diff, rev.ConfigSnapshot, rev.ValidationResult)
	return err
}

// GetConfigRevisions returns recent revisions for a service.
func (s *Store) GetConfigRevisions(serviceID string, limit int) ([]model.ConfigRevision, error) {
	rows, err := s.db.Query(
		`SELECT id, service_id, timestamp, actor, diff, config_snapshot, validation_result
		 FROM config_revisions WHERE service_id = ?
		 ORDER BY timestamp DESC LIMIT ?`,
		serviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var revs []model.ConfigRevision
	for rows.Next() {
		var r model.ConfigRevision
		var ts string
		if err := rows.Scan(&r.ID, &r.ServiceID, &ts, &r.Actor, &r.Diff, &r.ConfigSnapshot, &r.ValidationResult); err != nil {
			return nil, err
		}
		r.Timestamp, _ = time.Parse("2006-01-02 15:04:05", ts)
		revs = append(revs, r)
	}
	return revs, rows.Err()
}

// UpsertAlert creates or updates an alert.
func (s *Store) UpsertAlert(a *model.Alert) error {
	// Check if an active alert of this type+service already exists
	var id int64
	err := s.db.QueryRow(
		`SELECT id FROM alerts WHERE type = ? AND service_id = ? AND resolved = 0`,
		a.Type, a.ServiceID).Scan(&id)

	if err == sql.ErrNoRows {
		_, err = s.db.Exec(
			`INSERT INTO alerts (type, severity, service_id, message) VALUES (?, ?, ?, ?)`,
			a.Type, a.Severity, a.ServiceID, a.Message)
		return err
	}
	if err != nil {
		return err
	}

	// Update existing
	_, err = s.db.Exec(
		`UPDATE alerts SET severity = ?, message = ?, last_seen = datetime('now') WHERE id = ?`,
		a.Severity, a.Message, id)
	return err
}

// ResolveAlerts marks all active alerts of a type+service as resolved.
func (s *Store) ResolveAlerts(alertType, serviceID string) error {
	_, err := s.db.Exec(
		`UPDATE alerts SET resolved = 1, resolved_at = datetime('now')
		 WHERE type = ? AND service_id = ? AND resolved = 0`,
		alertType, serviceID)
	return err
}

// GetActiveAlerts returns all unresolved alerts.
func (s *Store) GetActiveAlerts() ([]model.Alert, error) {
	return s.queryAlerts(`SELECT id, type, severity, service_id, message, first_seen, last_seen, resolved, resolved_at
		FROM alerts WHERE resolved = 0 ORDER BY last_seen DESC`)
}

// GetAllAlerts returns all alerts (including resolved).
func (s *Store) GetAllAlerts(limit int) ([]model.Alert, error) {
	return s.queryAlerts(fmt.Sprintf(
		`SELECT id, type, severity, service_id, message, first_seen, last_seen, resolved, resolved_at
		 FROM alerts ORDER BY last_seen DESC LIMIT %d`, limit))
}

func (s *Store) queryAlerts(query string) ([]model.Alert, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []model.Alert
	for rows.Next() {
		var a model.Alert
		var firstSeen, lastSeen string
		var resolvedAt sql.NullString
		var serviceID sql.NullString
		if err := rows.Scan(&a.ID, &a.Type, &a.Severity, &serviceID, &a.Message,
			&firstSeen, &lastSeen, &a.Resolved, &resolvedAt); err != nil {
			slog.Error("scan alert", "err", err)
			continue
		}
		a.ServiceID = serviceID.String
		a.FirstSeen, _ = time.Parse("2006-01-02 15:04:05", firstSeen)
		a.LastSeen, _ = time.Parse("2006-01-02 15:04:05", lastSeen)
		if resolvedAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", resolvedAt.String)
			a.ResolvedAt = &t
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}
