package templates

import "truffels-api/internal/model"

var Electrs = model.ServiceTemplate{
	ID:               "electrs",
	DisplayName:      "electrs",
	Description:      "Electrum Rust Server — Bitcoin address index for wallets and block explorers",
	ContainerNames:   []string{"truffels-electrs"},
	Dependencies:     []string{"bitcoind"},
	Port:             "50001 (Electrum)",
	MemoryLimit:      "2048M",
	ConfigPath:       "electrs/electrs.toml",
	RequiresUnpruned: true,
	UpdateSource: &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"getumbrel/electrs"},
	},
	DataDirs: []model.DataDir{
		{
			Path:        "/srv/truffels/data/electrs/db",
			Label:       "Address index",
			Description: "RocksDB address index. Not clearable — would force ~6 h reindex.",
			Clearable:   false,
		},
	},
}
