package templates

import "truffels-api/internal/model"

var Ckpool = model.ServiceTemplate{
	ID:             "ckpool",
	DisplayName:    "ckpool",
	Description:    "Solo mining pool connected to Bitcoin Core",
	ContainerNames: []string{"truffels-ckpool"},
	Dependencies:   []string{"bitcoind"},
	MemoryLimit:    "256M",
	ConfigPath:     "ckpool/ckpool.conf",
	Port:           "3333 (stratum)",
	UpdateSource: &model.UpdateSource{
		Type:       model.SourceBitbucket,
		Repo:       "ckolivas/ckpool",
		Branch:     "master",
		NeedsBuild: true,
	},
}
