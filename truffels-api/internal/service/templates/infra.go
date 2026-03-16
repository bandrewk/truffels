package templates

import "truffels-api/internal/model"

var Proxy = model.ServiceTemplate{
	ID:             "proxy",
	DisplayName:    "Reverse Proxy",
	Description:    "Caddy reverse proxy for all web services",
	ContainerNames: []string{"truffels-proxy"},
	Dependencies:   nil,
	MemoryLimit:    "128M",
	Port:           "80 (HTTP)",
	ReadOnly:       true,
	UpdateSource: &model.UpdateSource{
		Type:      model.SourceDockerHub,
		Images:    []string{"caddy"},
		TagFilter: "2-alpine",
	},
}

var MempoolDB = model.ServiceTemplate{
	ID:             "mempool-db",
	DisplayName:    "mempool.space DB",
	Description:    "MariaDB database for mempool",
	ComposeDir:     "mempool",
	ContainerNames: []string{"truffels-mempool-db"},
	Dependencies:   nil,
	MemoryLimit:    "512M",
	ReadOnly:       true,
	FloatingTag:    true,
	UpdateSource: &model.UpdateSource{
		Type:      model.SourceDockerDigest,
		Images:    []string{"mariadb"},
		TagFilter: "lts",
	},
}

var CkstatsDB = model.ServiceTemplate{
	ID:             "ckstats-db",
	DisplayName:    "ckstats DB",
	Description:    "PostgreSQL database for ckstats",
	ComposeDir:     "ckstats",
	ContainerNames: []string{"truffels-ckstats-db"},
	Dependencies:   nil,
	MemoryLimit:    "256M",
	ReadOnly:       true,
	UpdateSource: &model.UpdateSource{
		Type:      model.SourceDockerHub,
		Images:    []string{"postgres"},
		TagFilter: "16-alpine",
	},
}

// Truffels is the unified entry for the truffels stack (agent + api + web).
var Truffels = model.ServiceTemplate{
	ID:             "truffels",
	DisplayName:    "Truffels",
	Description:    "Truffels stack (agent, API, web UI)",
	ComposeDir:     "truffels",
	ContainerNames: []string{"truffels-agent", "truffels-api", "truffels-web"},
	Port:           "8080 (API + web), 9090 (agent)",
	Dependencies:   nil,
	ReadOnly:       false,
	UpdateSource: &model.UpdateSource{
		Type:       model.SourceGitHubRelease,
		Repo:       "bandrewk/truffels",
		Images:     []string{"truffels/agent", "truffels/api", "truffels/web"},
		NeedsBuild: true,
	},
}
