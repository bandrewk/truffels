package templates

import "truffels-api/internal/model"

var Ckstats = model.ServiceTemplate{
	ID:             "ckstats",
	DisplayName:    "ckstats",
	Description:    "Mining stats dashboard for ckpool",
	ContainerNames: []string{"truffels-ckstats", "truffels-ckstats-cron"},
	Dependencies:   []string{"ckpool", "ckstats-db"},
	RequiresSynced: true,
	MemoryLimit:    "1024M",
	ConfigPath:     "",
	Port:           "80/ckstats (via proxy)",
	UpdateSource: &model.UpdateSource{
		Type:       model.SourceGitHub,
		Repo:       "mrv777/ckstats",
		Branch:     "main",
		NeedsBuild: true,
		RefScheme:  model.RefSchemeCommit,
		RepoDir:    "/srv/truffels/data/ckpoolstats",
	},
	DataDirs: []model.DataDir{
		{
			Path:        "/srv/truffels/data/ckstats/postgres",
			Label:       "Mining stats DB",
			Description: "PostgreSQL — persistent mining stats history. Not clearable.",
			Clearable:   false,
		},
	},
}
