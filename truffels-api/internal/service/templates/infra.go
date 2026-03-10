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
	ReadOnly:       true,
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
	ReadOnly:       true,
}
