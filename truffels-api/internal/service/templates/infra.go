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

var TruffelsAgent = model.ServiceTemplate{
	ID:             "truffels-agent",
	DisplayName:    "Truffels Agent",
	Description:    "Privileged Docker mediator",
	ComposeDir:     "truffels",
	ContainerNames: []string{"truffels-agent"},
	Dependencies:   nil,
	MemoryLimit:    "128M",
	Port:           "9090 (internal)",
	ReadOnly:       false,
	UpdateSource: &model.UpdateSource{
		Type:       model.SourceGitHubRelease,
		Repo:       "bandrewk/truffels",
		Images:     []string{"truffels/agent"},
		NeedsBuild: true,
	},
}

var TruffelsAPI = model.ServiceTemplate{
	ID:             "truffels-api",
	DisplayName:    "Truffels API",
	Description:    "Go control plane backend",
	ComposeDir:     "truffels",
	ContainerNames: []string{"truffels-api"},
	Dependencies:   nil,
	MemoryLimit:    "256M",
	Port:           "8080 (internal)",
	ReadOnly:       false,
	UpdateSource: &model.UpdateSource{
		Type:       model.SourceGitHubRelease,
		Repo:       "bandrewk/truffels",
		Images:     []string{"truffels/api"},
		NeedsBuild: true,
	},
}

var TruffelsWeb = model.ServiceTemplate{
	ID:             "truffels-web",
	DisplayName:    "Truffels Web",
	Description:    "React admin UI",
	ComposeDir:     "truffels",
	ContainerNames: []string{"truffels-web"},
	Dependencies:   nil,
	MemoryLimit:    "64M",
	Port:           "8080 (internal)",
	ReadOnly:       false,
	UpdateSource: &model.UpdateSource{
		Type:       model.SourceGitHubRelease,
		Repo:       "bandrewk/truffels",
		Images:     []string{"truffels/web"},
		NeedsBuild: true,
	},
}
