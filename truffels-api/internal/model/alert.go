package model

import "time"

type AlertSeverity string

const (
	SeverityWarning  AlertSeverity = "warning"
	SeverityCritical AlertSeverity = "critical"
)

type Alert struct {
	ID         int64         `json:"id"`
	Type       string        `json:"type"`
	Severity   AlertSeverity `json:"severity"`
	ServiceID  string        `json:"service_id,omitempty"`
	Message    string        `json:"message"`
	FirstSeen  time.Time     `json:"first_seen"`
	LastSeen   time.Time     `json:"last_seen"`
	Resolved   bool          `json:"resolved"`
	ResolvedAt *time.Time    `json:"resolved_at,omitempty"`
}
