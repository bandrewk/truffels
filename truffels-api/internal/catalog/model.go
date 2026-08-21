package catalog

// Entry mirrors the catalog schema of the agent. The API reads the catalog via
// GET /v1/catalog and does not keep its own copy of the files — this prevents
// any version skew between the two.
type Entry struct {
	SchemaVersion  int    `json:"schema_version"`
	ID             string `json:"id"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
	Role           string `json:"role"`
	Implementation string `json:"implementation"`
	Chain          string `json:"chain,omitempty"`

	// Params is the install-parameter schema the web dialog builds its form
	// from. Without it the install dialog renders no fields.
	Params []struct {
		Name        string   `json:"name"`
		Description string   `json:"description,omitempty"`
		Type        string   `json:"type"`
		Default     any      `json:"default,omitempty"`
		Min         *int     `json:"min,omitempty"`
		Max         *int     `json:"max,omitempty"`
		Enum        []string `json:"enum,omitempty"`
	} `json:"params,omitempty"`

	Containers []struct {
		Name          string `json:"name"`
		MemoryLimitMB int    `json:"memory_limit_mb"`
	} `json:"containers"`

	Requires []struct {
		Role string `json:"role"`
		ID   string `json:"id,omitempty"`
	} `json:"requires,omitempty"`

	Source *struct {
		Kind string `json:"kind"`
		From string `json:"from"`
	} `json:"source,omitempty"`

	// ChainInfo marks an entry as a chain node whose sync state can be probed.
	// SyncProbe's presence is the signal the API uses to decide whether to ask
	// the agent for this service's sync status.
	ChainInfo *struct {
		RPCStyle  string   `json:"rpc_style"`
		SyncProbe []string `json:"sync_probe,omitempty"`
	} `json:"chain_info,omitempty"`

	Resources struct {
		MinDiskGB     int `json:"min_disk_gb,omitempty"`
		MemoryFloorMB int `json:"memory_floor_mb"`
	} `json:"resources"`

	// ContainerNamesField holds the container names as reported by the agent.
	// Derivation lives in the agent only; the API consumes what /v1/catalog reports.
	ContainerNamesField []string `json:"container_names"`
}

// ContainerNames returns the container names reported by the agent.
// No derivation happens here — the agent is the source of truth.
func (e Entry) ContainerNames() []string {
	return e.ContainerNamesField
}
