package model

import "time"

type ServiceState string

const (
	StateRunning  ServiceState = "running"
	StateStopped  ServiceState = "stopped"
	StateDegraded ServiceState = "degraded"
	StateUnknown   ServiceState = "unknown"
	StateDisabled  ServiceState = "disabled"
)

type ServiceTemplate struct {
	ID             string   `json:"id"`
	DisplayName    string   `json:"display_name"`
	Description    string   `json:"description"`
	ComposeDir     string   `json:"-"`
	ContainerNames []string `json:"container_names"`
	Dependencies   []string `json:"dependencies"`
	MemoryLimit    string   `json:"memory_limit"`
	ConfigPath     string   `json:"-"`
	Port           string        `json:"port,omitempty"`
	ReadOnly       bool          `json:"read_only,omitempty"`
	FloatingTag      bool          `json:"floating_tag,omitempty"`
	RequiresUnpruned bool          `json:"requires_unpruned,omitempty"`
	UpdateSource     *UpdateSource `json:"update_source,omitempty"`
}

type ContainerState struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	Health       string `json:"health"`
	RestartCount int    `json:"restart_count"`
	StartedAt    string `json:"started_at"`
	Image        string `json:"image"`
}

type SyncInfo struct {
	Syncing  bool    `json:"syncing"`
	Progress float64 `json:"progress"` // 0.0–1.0
	Detail   string  `json:"detail"`   // e.g. "74.5%" or "1,234 blocks behind"
}

type ServiceInstance struct {
	Template         ServiceTemplate  `json:"template"`
	State            ServiceState     `json:"state"`
	Enabled          bool             `json:"enabled"`
	Containers       []ContainerState `json:"containers"`
	LastHealthCheck  time.Time        `json:"last_health_check"`
	DependencyIssues []string         `json:"dependency_issues,omitempty"`
	SyncInfo         *SyncInfo        `json:"sync_info,omitempty"`
}
