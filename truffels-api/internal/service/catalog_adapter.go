package service

import (
	"strconv"

	"truffels-api/internal/catalog"
	"truffels-api/internal/model"
)

// CatalogEntryToTemplate projects an installed catalog entry onto the
// ServiceTemplate the rest of the control plane already understands, so the
// existing lifecycle handlers work for catalog services unchanged.
//
// UpdateSource is deliberately nil: the update path for catalog services is
// separate (spec 8.1), and the update guards already refuse the legacy path
// for these ids.
func CatalogEntryToTemplate(e catalog.Entry, composeRoot, dataRoot string) model.ServiceTemplate {
	dataDir := dataRoot + "/cat-" + e.ID
	tmpl := model.ServiceTemplate{
		ID:             e.ID,
		DisplayName:    e.DisplayName,
		Description:    e.Description,
		ComposeDir:     composeRoot + "/cat-" + e.ID,
		ContainerNames: e.ContainerNames(),
		UpdateSource:   nil,
		DataDirs: []model.DataDir{{
			Path:      dataDir,
			Label:     e.DisplayName + " data",
			Clearable: true,
		}},
	}
	if len(e.Containers) > 0 {
		// memory limit string form matches the compose templates ("2048M")
		if mb := e.Containers[0].MemoryLimitMB; mb > 0 {
			tmpl.MemoryLimit = strconv.Itoa(mb) + "M"
		}
	}
	// A catalog entry's Requires become ordinary template dependencies, so the
	// existing "dependency must be running" check in the start handler covers
	// catalog stacks unchanged (e.g. ckpool-dgb requires digibyted).
	for _, req := range e.Requires {
		if req.ID != "" {
			tmpl.Dependencies = append(tmpl.Dependencies, req.ID)
		}
	}
	// A web entry gets its route projected with the full container name (the
	// same "truffels-<id>-<container>" derivation the agent uses) so the proxy
	// can reverse_proxy to it and the UI can link to it.
	if e.Web != nil {
		tmpl.Web = &model.WebRoute{
			Route:     e.Web.Route,
			Container: "truffels-" + e.ID + "-" + e.Web.Container,
			Port:      e.Web.Port,
		}
	}
	return tmpl
}
