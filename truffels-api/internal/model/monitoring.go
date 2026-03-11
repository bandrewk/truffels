package model

import "time"

// MetricSnapshot is a single recorded point of host resource usage.
type MetricSnapshot struct {
	ID          int64     `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	CPUPercent  float64   `json:"cpu_percent"`
	MemPercent  float64   `json:"mem_percent"`
	TempC       float64   `json:"temp_c"`
	DiskPercent float64   `json:"disk_percent"`
}

// ServiceEvent records a container state/health change or restart.
type ServiceEvent struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	ServiceID string    `json:"service_id"`
	Container string    `json:"container"`
	EventType string    `json:"event_type"`
	FromState string    `json:"from_state"`
	ToState   string    `json:"to_state"`
	Message   string    `json:"message"`
}

// MetricsSummary holds computed averages and peaks for a time range.
type MetricsSummary struct {
	CPUAvg  float64 `json:"cpu_avg"`
	CPUMax  float64 `json:"cpu_max"`
	MemAvg  float64 `json:"mem_avg"`
	MemMax  float64 `json:"mem_max"`
	TempAvg float64 `json:"temp_avg"`
	TempMax float64 `json:"temp_max"`
}

// MonitoringMetrics combines current, historical, and summary metrics.
type MonitoringMetrics struct {
	Current *HostMetrics     `json:"current"`
	History []MetricSnapshot `json:"history"`
	Summary MetricsSummary   `json:"summary"`
}

// MonitoringContainer describes a single container for the monitoring view.
type MonitoringContainer struct {
	Name         string `json:"name"`
	ServiceID    string `json:"service_id"`
	DisplayName  string `json:"display_name"`
	Status       string `json:"status"`
	Health       string `json:"health"`
	RestartCount int    `json:"restart_count"`
	StartedAt    string `json:"started_at"`
	Image        string `json:"image"`
}

// MonitoringResponse is the full payload for GET /monitoring.
type MonitoringResponse struct {
	Containers []MonitoringContainer `json:"containers"`
	Events     []ServiceEvent       `json:"events"`
	Metrics    MonitoringMetrics     `json:"metrics"`
	Alerts     []Alert              `json:"alerts"`
}
