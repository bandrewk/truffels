package store

import (
	"database/sql"
	"time"

	"truffels-api/internal/model"
)

// UpsertUpdateCheck creates or replaces the latest update check for a service.
func (s *Store) UpsertUpdateCheck(c *model.UpdateCheck) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec(`DELETE FROM update_checks WHERE service_id = ?`, c.ServiceID)
	_, err = tx.Exec(
		`INSERT INTO update_checks (service_id, current_version, latest_version, has_update, error)
		 VALUES (?, ?, ?, ?, ?)`,
		c.ServiceID, c.CurrentVersion, c.LatestVersion, boolToInt(c.HasUpdate), c.Error)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetLatestUpdateCheck returns the most recent check for a service.
func (s *Store) GetLatestUpdateCheck(serviceID string) (*model.UpdateCheck, error) {
	var c model.UpdateCheck
	var checkedAt string
	var hasUpdate int
	err := s.db.QueryRow(
		`SELECT id, service_id, current_version, latest_version, has_update, checked_at, error
		 FROM update_checks WHERE service_id = ? ORDER BY checked_at DESC LIMIT 1`,
		serviceID).Scan(&c.ID, &c.ServiceID, &c.CurrentVersion, &c.LatestVersion,
		&hasUpdate, &checkedAt, &c.Error)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.HasUpdate = hasUpdate == 1
	c.CheckedAt, _ = time.Parse("2006-01-02 15:04:05", checkedAt)
	return &c, nil
}

// GetAllUpdateChecks returns the latest check for every service.
func (s *Store) GetAllUpdateChecks() ([]model.UpdateCheck, error) {
	rows, err := s.db.Query(
		`SELECT c.id, c.service_id, c.current_version, c.latest_version, c.has_update, c.checked_at, c.error
		 FROM update_checks c
		 INNER JOIN (
		   SELECT service_id, MAX(checked_at) AS max_at FROM update_checks GROUP BY service_id
		 ) m ON c.service_id = m.service_id AND c.checked_at = m.max_at
		 ORDER BY c.service_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var checks []model.UpdateCheck
	for rows.Next() {
		var c model.UpdateCheck
		var checkedAt string
		var hasUpdate int
		if err := rows.Scan(&c.ID, &c.ServiceID, &c.CurrentVersion, &c.LatestVersion,
			&hasUpdate, &checkedAt, &c.Error); err != nil {
			continue
		}
		c.HasUpdate = hasUpdate == 1
		c.CheckedAt, _ = time.Parse("2006-01-02 15:04:05", checkedAt)
		checks = append(checks, c)
	}
	return checks, rows.Err()
}

// CreateUpdateLog inserts a new update log entry and returns its ID.
func (s *Store) CreateUpdateLog(l *model.UpdateLog) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO update_log (service_id, from_version, to_version, status)
		 VALUES (?, ?, ?, ?)`,
		l.ServiceID, l.FromVersion, l.ToVersion, l.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateLogStatus updates the status (and optionally error/rollback) of an update log entry.
func (s *Store) UpdateLogStatus(id int64, status model.UpdateStatus, errMsg, rollbackVersion string) error {
	_, err := s.db.Exec(
		`UPDATE update_log SET status = ?, completed_at = datetime('now'), error = ?, rollback_version = ?
		 WHERE id = ?`,
		status, errMsg, rollbackVersion, id)
	return err
}

// GetUpdateLogs returns recent update logs for a service (or all if serviceID is empty).
func (s *Store) GetUpdateLogs(serviceID string, limit int) ([]model.UpdateLog, error) {
	var query string
	var args []interface{}
	if serviceID != "" {
		query = `SELECT id, service_id, from_version, to_version, status, started_at, completed_at, error, rollback_version
				 FROM update_log WHERE service_id = ? ORDER BY started_at DESC LIMIT ?`
		args = []interface{}{serviceID, limit}
	} else {
		query = `SELECT id, service_id, from_version, to_version, status, started_at, completed_at, error, rollback_version
				 FROM update_log ORDER BY started_at DESC LIMIT ?`
		args = []interface{}{limit}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []model.UpdateLog
	for rows.Next() {
		var l model.UpdateLog
		var startedAt string
		var completedAt sql.NullString
		if err := rows.Scan(&l.ID, &l.ServiceID, &l.FromVersion, &l.ToVersion, &l.Status,
			&startedAt, &completedAt, &l.Error, &l.RollbackVersion); err != nil {
			continue
		}
		l.StartedAt, _ = time.Parse("2006-01-02 15:04:05", startedAt)
		if completedAt.Valid {
			t, _ := time.Parse("2006-01-02 15:04:05", completedAt.String)
			l.CompletedAt = &t
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// PendingUpdateCount returns the number of services with available updates.
func (s *Store) PendingUpdateCount() (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(DISTINCT c.service_id) FROM update_checks c
		 INNER JOIN (
		   SELECT service_id, MAX(checked_at) AS max_at FROM update_checks GROUP BY service_id
		 ) m ON c.service_id = m.service_id AND c.checked_at = m.max_at
		 WHERE c.has_update = 1 AND c.error = ''`).Scan(&count)
	return count, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
