package store

import (
	"time"

	"truffels-api/internal/model"
)

// InsertMetricSnapshot records a point-in-time host resource snapshot.
func (s *Store) InsertMetricSnapshot(cpu, mem, temp, disk float64, fanRPM, fanPercent int) error {
	_, err := s.db.Exec(
		`INSERT INTO metric_snapshots (cpu_percent, mem_percent, temp_c, disk_percent, fan_rpm, fan_percent)
		 VALUES (?, ?, ?, ?, ?, ?)`, cpu, mem, temp, disk, fanRPM, fanPercent)
	return err
}

// GetMetricSnapshots returns snapshots since the given time, downsampled to at most maxRows points.
func (s *Store) GetMetricSnapshots(since time.Time, maxRows int) ([]model.MetricSnapshot, error) {
	sinceStr := since.UTC().Format("2006-01-02 15:04:05")

	rows, err := s.db.Query(
		`SELECT id, timestamp, cpu_percent, mem_percent, temp_c, disk_percent, fan_rpm, fan_percent
		 FROM metric_snapshots WHERE timestamp >= ?
		 ORDER BY timestamp ASC`, sinceStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []model.MetricSnapshot
	for rows.Next() {
		var snap model.MetricSnapshot
		var ts string
		if err := rows.Scan(&snap.ID, &ts, &snap.CPUPercent, &snap.MemPercent, &snap.TempC, &snap.DiskPercent, &snap.FanRPM, &snap.FanPercent); err != nil {
			continue
		}
		snap.Timestamp, _ = time.Parse("2006-01-02 15:04:05", ts)
		all = append(all, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Downsample in Go: pick every Nth row to get ~maxRows points
	if len(all) <= maxRows {
		return all, nil
	}

	step := len(all) / maxRows
	if step < 1 {
		step = 1
	}
	sampled := make([]model.MetricSnapshot, 0, maxRows+1)
	for i := 0; i < len(all); i += step {
		sampled = append(sampled, all[i])
	}
	// Always include the last point
	if len(all) > 0 && sampled[len(sampled)-1].ID != all[len(all)-1].ID {
		sampled = append(sampled, all[len(all)-1])
	}
	return sampled, nil
}

// PruneMetricSnapshots deletes snapshots older than the given time.
func (s *Store) PruneMetricSnapshots(olderThan time.Time) error {
	ts := olderThan.UTC().Format("2006-01-02 15:04:05")
	_, err := s.db.Exec(`DELETE FROM metric_snapshots WHERE timestamp < ?`, ts)
	return err
}

// InsertServiceEvent records a container state change, health change, or restart event.
func (s *Store) InsertServiceEvent(serviceID, container, eventType, fromState, toState, message string) error {
	_, err := s.db.Exec(
		`INSERT INTO service_events (service_id, container, event_type, from_state, to_state, message)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		serviceID, container, eventType, fromState, toState, message)
	return err
}

// GetServiceEvents returns recent events since the given time, newest first.
func (s *Store) GetServiceEvents(since time.Time, limit int) ([]model.ServiceEvent, error) {
	sinceStr := since.UTC().Format("2006-01-02 15:04:05")

	rows, err := s.db.Query(
		`SELECT id, timestamp, service_id, container, event_type, from_state, to_state, message
		 FROM service_events WHERE timestamp >= ?
		 ORDER BY timestamp DESC LIMIT ?`, sinceStr, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.ServiceEvent
	for rows.Next() {
		var ev model.ServiceEvent
		var ts string
		if err := rows.Scan(&ev.ID, &ts, &ev.ServiceID, &ev.Container, &ev.EventType,
			&ev.FromState, &ev.ToState, &ev.Message); err != nil {
			continue
		}
		ev.Timestamp, _ = time.Parse("2006-01-02 15:04:05", ts)
		events = append(events, ev)
	}
	return events, rows.Err()
}

// PruneServiceEvents keeps only the most recent keepN events, deleting the rest.
func (s *Store) PruneServiceEvents(keepN int) error {
	_, err := s.db.Exec(
		`DELETE FROM service_events WHERE id NOT IN (
		   SELECT id FROM service_events ORDER BY timestamp DESC LIMIT ?
		 )`, keepN)
	return err
}
