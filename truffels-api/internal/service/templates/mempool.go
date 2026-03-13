package templates

import "truffels-api/internal/model"

var Mempool = model.ServiceTemplate{
	ID:             "mempool",
	DisplayName:    "mempool.space",
	Description:    "Bitcoin block explorer and mempool visualizer",
	ContainerNames: []string{"truffels-mempool-backend", "truffels-mempool-frontend"},
	Dependencies:   []string{"bitcoind", "electrs", "mempool-db"},
	MemoryLimit:    "1792M",
	ConfigPath:       "",
	RequiresUnpruned: true,
	Port:           "80 (via proxy)",
	UpdateSource: &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"mempool/backend", "mempool/frontend"},
	},
}
