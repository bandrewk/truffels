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

	Resources struct {
		MinDiskGB     int `json:"min_disk_gb,omitempty"`
		MemoryFloorMB int `json:"memory_floor_mb"`
	} `json:"resources"`
}

// ContainerNames returns the derived container names. Same rule as in the
// agent (catalog_derive.go): truffels-<id>-<container>.
func (e Entry) ContainerNames() []string {
	out := make([]string, 0, len(e.Containers))
	for _, c := range e.Containers {
		out = append(out, "truffels-"+e.ID+"-"+c.Name)
	}
	return out
}
