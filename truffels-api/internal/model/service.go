package model

import "time"

type ServiceState string

const (
	StateRunning  ServiceState = "running"
	StateStopped  ServiceState = "stopped"
	StateDegraded ServiceState = "degraded"
	StateUnknown  ServiceState = "unknown"
	StateDisabled ServiceState = "disabled"
)

type ServiceTemplate struct {
	ID               string        `json:"id"`
	DisplayName      string        `json:"display_name"`
	Description      string        `json:"description"`
	ComposeDir       string        `json:"-"`
	ContainerNames   []string      `json:"container_names"`
	Dependencies     []string      `json:"dependencies"`
	MemoryLimit      string        `json:"memory_limit"`
	ConfigPath       string        `json:"-"`
	Port             string        `json:"port,omitempty"`
	ReadOnly         bool          `json:"read_only,omitempty"`
	FloatingTag      bool          `json:"floating_tag,omitempty"`
	RequiresUnpruned bool          `json:"requires_unpruned,omitempty"`
	RequiresSynced   bool          `json:"requires_synced,omitempty"`
	UpdateSource     *UpdateSource `json:"update_source,omitempty"`
	StackContainers  []string      `json:"stack_containers,omitempty"`

	// EnsureDirs are host directories the compose reconciler creates with
	// the specified ownership before running `docker compose up` — used for
	// bind-mount sources that need a specific uid:gid.
	EnsureDirs []EnsureDir `json:"-"`

	// DataDirs are host directories where this service stores persistent state.
	// Surfaced in the Service Data Storage panel. Clearable entries get a Clear
	// button in the UI; non-clearable ones are view-only.
	DataDirs []DataDir `json:"data_dirs,omitempty"`
}

// EnsureDir describes a directory the reconciler must create before bringing
// the service up. The reconciler calls the agent's POST /v1/fs/ensure-dir
// with these parameters.
type EnsureDir struct {
	Path string `json:"path"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
	Mode string `json:"mode"` // octal string, e.g. "0755"
}

// DataDir describes a service's persistent host directory for the UI.
type DataDir struct {
	Path         string `json:"path"`
	Label        string `json:"label"`
	Description  string `json:"description"` // shown as warning text in Clear confirmation
	Clearable    bool   `json:"clearable"`
	RequiresStop bool   `json:"requires_stop,omitempty"` // service must be stopped before clear
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
