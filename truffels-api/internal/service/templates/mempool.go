package templates

import "truffels-api/internal/model"

var Mempool = model.ServiceTemplate{
	ID:             "mempool",
	DisplayName:    "mempool.space",
	Description:    "Bitcoin block explorer and mempool visualizer",
	ContainerNames: []string{"truffels-mempool-backend", "truffels-mempool-frontend"},
	Dependencies:   []string{"bitcoind", "electrs", "mempool-db"},
	MemoryLimit:    "2304M",
	ConfigPath:       "",
	RequiresUnpruned: true,
	Port:           "80 (via proxy)",
	UpdateSource: &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"mempool/backend", "mempool/frontend"},
	},
	EnsureDirs: []model.EnsureDir{
		// Bind-mount source for the backend's RBF/mempool cache. Created by
		// the agent before `compose up` so the in-container uid 1000 process
		// can write to it. See v0.3.1-dev.14 OOM fix.
		{Path: "/srv/truffels/data/mempool/cache", UID: 1000, GID: 1000, Mode: "0755"},
	},
	DataDirs: []model.DataDir{
		{
			Path:        "/srv/truffels/data/mempool/cache",
			Label:       "Mempool cache",
			Description: "RBF + mempool state cache. Safe to clear — rebuilt from electrs on next start. The mempool backend will be briefly stopped during clear.",
			Clearable:   true,
			RequiresStop: true,
		},
		{
			Path:        "/srv/truffels/data/mempool/mysql",
			Label:       "Mempool DB",
			Description: "Historical statistics (mariadb). Not safe to clear — loses chart history.",
			Clearable:   false,
		},
	},
}
