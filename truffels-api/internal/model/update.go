package model

import "time"

type UpdateStatus string

const (
	UpdatePending    UpdateStatus = "pending"
	UpdatePulling    UpdateStatus = "pulling"
	UpdateBuilding   UpdateStatus = "building"
	UpdateRestarting UpdateStatus = "restarting"
	UpdateDone       UpdateStatus = "done"
	UpdateFailed     UpdateStatus = "failed"
	UpdateRolledBack UpdateStatus = "rolled_back"
)

type SourceType string

const (
	SourceDockerHub  SourceType = "dockerhub"
	SourceGitHub     SourceType = "github"
	SourceBitbucket  SourceType = "bitbucket"
)

// UpdateSource defines where a service gets its updates from.
type UpdateSource struct {
	Type       SourceType `json:"type"`
	Images     []string   `json:"images,omitempty"`     // dockerhub: ["mempool/backend","mempool/frontend"]
	Repo       string     `json:"repo,omitempty"`       // github/bitbucket: "owner/repo"
	Branch     string     `json:"branch,omitempty"`     // github/bitbucket: "main" or "master"
	NeedsBuild bool       `json:"needs_build"`          // true for custom-built images (ckpool, ckstats)
	TagFilter  string     `json:"tag_filter,omitempty"` // dockerhub: only consider tags matching this prefix (e.g. "2.9-alpine", "16-alpine")
}

// UpdateCheck represents the latest known version info for a service.
type UpdateCheck struct {
	ID             int64      `json:"id"`
	ServiceID      string     `json:"service_id"`
	CurrentVersion string     `json:"current_version"`  // current tag or commit hash
	LatestVersion  string     `json:"latest_version"`   // latest tag or commit hash
	HasUpdate      bool       `json:"has_update"`
	CheckedAt      time.Time  `json:"checked_at"`
	Error          string     `json:"error,omitempty"`
}

// PreflightResult holds the outcome of pre-update checks for a service.
type PreflightResult struct {
	ServiceID   string           `json:"service_id"`
	FromVersion string           `json:"from_version"`
	ToVersion   string           `json:"to_version"`
	CanProceed  bool             `json:"can_proceed"`
	Checks      []PreflightCheck `json:"checks"`
}

// PreflightCheck is a single pass/fail/warn check within a preflight result.
type PreflightCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`   // "pass", "fail", "warn"
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

// UpdateLog records an update attempt.
type UpdateLog struct {
	ID              int64        `json:"id"`
	ServiceID       string       `json:"service_id"`
	FromVersion     string       `json:"from_version"`
	ToVersion       string       `json:"to_version"`
	Status          UpdateStatus `json:"status"`
	StartedAt       time.Time    `json:"started_at"`
	CompletedAt     *time.Time   `json:"completed_at,omitempty"`
	Error           string       `json:"error,omitempty"`
	RollbackVersion string       `json:"rollback_version,omitempty"`
}
