package templates

import "truffels-api/internal/model"

var Ckpool = model.ServiceTemplate{
	ID:             "ckpool",
	DisplayName:    "ckpool",
	Description:    "Solo mining pool connected to Bitcoin Core",
	ContainerNames: []string{"truffels-ckpool"},
	Dependencies:   []string{"bitcoind"},
	RequiresSynced: true,
	MemoryLimit:    "1024M",
	ConfigPath:     "ckpool/ckpool.conf",
	Port:           "3333 (stratum)",
	UpdateSource: &model.UpdateSource{
		Type:       model.SourceBitbucket,
		Repo:       "ckolivas/ckpool",
		Branch:     "master",
		NeedsBuild: true,
		RefScheme:  model.RefSchemeTag,
		TagFilter:  "v",
	},
	DataDirs: []model.DataDir{
		{
			Path:        "/srv/truffels/data/ckpool",
			Label:       "Ckpool state + logs",
			Description: "Pool state, share data, and per-miner logs. Clearing logs separately is a future feature; today the whole dir is view-only.",
			Clearable:   false,
		},
	},
}
