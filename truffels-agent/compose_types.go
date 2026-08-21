package main

// The output format of the generator.
//
// These structs ARE the allowlist. Fields like privileged, cap_add, pid, ipc,
// network_mode, userns_mode, devices, or a Docker socket mount do not exist
// here — therefore there is no code path that could emit them.
// A deny list is kept incomplete; a missing structure is not.
//
// Serialization is done with encoding/json. This is no stopgap: YAML 1.2 is
// a superset of JSON, docker compose reads the file correctly, and json.Marshal
// escapes newlines and quotes structurally. A parameter value can therefore
// not open a new key. Verified on 2026-08-20 against
// `docker compose config`.
type composeFile struct {
	Name     string                    `json:"name"`
	Services map[string]composeService `json:"services"`
	Networks map[string]composeNetwork `json:"networks"`
}

type composeService struct {
	Image         string         `json:"image"`
	ContainerName string         `json:"container_name"`
	Restart       string         `json:"restart"`
	User          string         `json:"user,omitempty"`
	SecurityOpt   []string       `json:"security_opt"`
	CapDrop       []string       `json:"cap_drop"`
	Networks      []string       `json:"networks"`
	Ports         []string       `json:"ports,omitempty"`
	Volumes       []string       `json:"volumes,omitempty"`
	Entrypoint    []string       `json:"entrypoint,omitempty"`
	DependsOn     []string       `json:"depends_on,omitempty"`
	Healthcheck   *composeHealth `json:"healthcheck,omitempty"`
	Deploy        composeDeploy  `json:"deploy"`
	Build         *composeBuild  `json:"build,omitempty"`
}

// composeBuild is emitted only for catalog entries that ship a Dockerfile
// instead of a pre-built image. It is a deliberate, bounded widening of the
// allowlist above: `context` is always rooted at the read-only /repo mount and
// the generator (catalogBuildBlock) rejects any dockerfile path that is
// absolute or escapes the repo, so a catalog entry cannot point the build at
// an arbitrary host path. The Dockerfile itself is curated content shipped in
// the repo, not a runtime parameter. `up -d` builds the image on first start
// when it is missing; on later starts the existing image is reused.
type composeBuild struct {
	Context    string `json:"context"`
	Dockerfile string `json:"dockerfile,omitempty"`
}

type composeHealth struct {
	Test        []string `json:"test"`
	Interval    string   `json:"interval"`
	Timeout     string   `json:"timeout"`
	Retries     int      `json:"retries"`
	StartPeriod string   `json:"start_period"`
}

type composeDeploy struct {
	Resources composeResources `json:"resources"`
}

type composeResources struct {
	Limits composeLimits `json:"limits"`
}

type composeLimits struct {
	Memory string `json:"memory"`
}

type composeNetwork struct {
	Name string `json:"name"`
	// External marks a network compose must NOT create or destroy: it is the
	// shared stack network, whose lifecycle the agent manages (ref-counted
	// across stack members). Only ever set for the stack network.
	External bool `json:"external,omitempty"`
}
