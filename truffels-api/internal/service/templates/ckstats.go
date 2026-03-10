package templates

import "truffels-api/internal/model"

var Ckstats = model.ServiceTemplate{
	ID:             "ckstats",
	DisplayName:    "ckstats",
	Description:    "Mining stats dashboard for ckpool",
	ContainerNames: []string{"truffels-ckstats", "truffels-ckstats-cron", "truffels-ckstats-db"},
	Dependencies:   []string{"ckpool"},
	MemoryLimit:    "1024M",
	ConfigPath:     "",
	Port:           "80/ckstats (via proxy)",
	UpdateSource: &model.UpdateSource{
		Type:       model.SourceGitHub,
		Repo:       "mrv777/ckstats",
		Branch:     "main",
		NeedsBuild: true,
	},
}
