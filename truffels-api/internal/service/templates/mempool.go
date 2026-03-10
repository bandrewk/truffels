package templates

import "truffels-api/internal/model"

var Mempool = model.ServiceTemplate{
	ID:             "mempool",
	DisplayName:    "mempool.space",
	Description:    "Bitcoin block explorer and mempool visualizer",
	ContainerNames: []string{"truffels-mempool-backend", "truffels-mempool-frontend", "truffels-mempool-db"},
	Dependencies:   []string{"bitcoind", "electrs"},
	MemoryLimit:    "1792M",
	ConfigPath:     "",
	Port:           "80 (via proxy)",
	UpdateSource: &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"mempool/backend", "mempool/frontend"},
	},
}
