package main

// CatalogEntry describes a service type. Role, implementation, and chain are
// three separate fields: role describes what the service is in the dependency
// graph, implementation describes how (which software), and chain describes
// what blockchain it serves. See Spec 4.3 — BCH uses asicseer-pool instead of
// ckpool; a schema with only `role` would create an entry that doesn't work.
type CatalogEntry struct {
	SchemaVersion  int    `json:"schema_version"`
	ID             string `json:"id"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
	Role           string `json:"role"`
	Implementation string `json:"implementation"`
	Chain          string `json:"chain,omitempty"`
	// Stack groups entries that must reach each other (node + pool + stats).
	// All members of a stack join a shared external network in addition to
	// their own isolated one, so they resolve each other by container name.
	Stack      string          `json:"stack,omitempty"`
	Image      string          `json:"image,omitempty"`
	Build      *BuildSpec      `json:"build,omitempty"`
	Params     []ParamSpec     `json:"params,omitempty"`
	Containers []ContainerSpec `json:"containers"`
	Requires   []RequireSpec   `json:"requires,omitempty"`
	Source     *SourceSpec     `json:"source,omitempty"`
	ChainInfo  *ChainSpec      `json:"chain_info,omitempty"`
	Resources  ResourceSpec    `json:"resources"`
}

type BuildSpec struct {
	Dockerfile string `json:"dockerfile"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256,omitempty"`
}

type ParamSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Type        string   `json:"type"` // "int" | "string" | "enum" | "bool" — parameter type constraint
	Default     any      `json:"default,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
}

type ContainerSpec struct {
	Name          string           `json:"name"`
	MemoryLimitMB int              `json:"memory_limit_mb"`
	User          string           `json:"user,omitempty"`
	Entrypoint    []string         `json:"entrypoint,omitempty"`
	Ports         []PortSpec       `json:"ports,omitempty"`
	Volumes       []VolumeSpec     `json:"volumes,omitempty"`
	Healthcheck   *HealthcheckSpec `json:"healthcheck,omitempty"`
	// EnvFile names a rendered config file (bare filename) whose KEY=VALUE lines
	// become this container's environment — e.g. postgres credentials.
	EnvFile string `json:"env_file,omitempty"`
}

type PortSpec struct {
	Container int    `json:"container"`
	Host      int    `json:"host"`
	Role      string `json:"role"`
}

// VolumeSpec never names a host path. Kind plus derived root determine it;
// an entry thus cannot mount `/`.
type VolumeSpec struct {
	Kind  string `json:"kind"` // "data" | "config" | "pool-logs"
	File  string `json:"file,omitempty"`
	Mount string `json:"mount"`
	RO    bool   `json:"ro,omitempty"`
	// From names another catalog entry whose data this volume borrows, used by
	// kind "pool-logs" so a stats service can read a pool's logs. It is a
	// catalog id (validated), never a host path.
	From string `json:"from,omitempty"`
}

type HealthcheckSpec struct {
	Test        []string `json:"test"`
	Interval    string   `json:"interval"`
	Timeout     string   `json:"timeout"`
	Retries     int      `json:"retries"`
	StartPeriod string   `json:"start_period"`
}

type RequireSpec struct {
	Role string `json:"role"`
	ID   string `json:"id,omitempty"`
}

// SourceSpec decouples stats services from the node. RPC access is never
// implicitly assumed — see Spec 4.4 and the BCH reference stack, where
// ckstats reads pool logs because BCHN has no rpcwhitelist.
type SourceSpec struct {
	Kind string `json:"kind"` // "pool-logs" | "rpc" | "http-metrics"
	From string `json:"from"`
}

type ChainSpec struct {
	P2PPort  int    `json:"p2p_port"`
	RPCPort  int    `json:"rpc_port"`
	RPCStyle string `json:"rpc_style"`
	// SyncProbe is the argv run inside the node's container to read its sync
	// state (bitcoin-core style: getblockchaininfo). It comes from the embedded
	// catalog, never from a request, so the chain-probe endpoint can never be
	// asked to run an arbitrary command.
	SyncProbe []string `json:"sync_probe,omitempty"`
}

type ResourceSpec struct {
	MinDiskGB     int `json:"min_disk_gb,omitempty"`
	MemoryFloorMB int `json:"memory_floor_mb"`
}
