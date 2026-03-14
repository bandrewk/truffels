package service

import (
	"fmt"
	"truffels-api/internal/model"
	"truffels-api/internal/service/templates"
)

type Registry struct {
	services map[string]model.ServiceTemplate
	order    []string // topological order
}

func NewRegistry(composeRoot, gitHubRepo string) *Registry {
	all := []model.ServiceTemplate{
		templates.Bitcoind,
		templates.Electrs,
		templates.Ckpool,
		templates.Mempool,
		templates.Ckstats,
		templates.Proxy,
		templates.MempoolDB,
		templates.CkstatsDB,
		templates.TruffelsAgent,
		templates.TruffelsAPI,
		templates.TruffelsWeb,
	}

	r := &Registry{
		services: make(map[string]model.ServiceTemplate, len(all)),
	}

	for _, svc := range all {
		dirName := svc.ID
		if svc.ComposeDir != "" {
			dirName = svc.ComposeDir
		}
		svc.ComposeDir = composeRoot + "/" + dirName
		// Override GitHub repo for self-update sources
		if svc.UpdateSource != nil && svc.UpdateSource.Type == model.SourceGitHubRelease && gitHubRepo != "" {
			src := *svc.UpdateSource
			src.Repo = gitHubRepo
			svc.UpdateSource = &src
		}
		r.services[svc.ID] = svc
	}

	// Fixed topological order for the dependency graph
	r.order = []string{"bitcoind", "electrs", "ckpool", "mempool-db", "ckstats-db", "mempool", "ckstats", "proxy", "truffels-agent", "truffels-api", "truffels-web"}

	// Compute stack containers: all containers sharing the same compose dir
	byDir := map[string][]string{}
	for _, svc := range r.services {
		byDir[svc.ComposeDir] = append(byDir[svc.ComposeDir], svc.ContainerNames...)
	}
	for id, svc := range r.services {
		stack := byDir[svc.ComposeDir]
		if len(stack) > 1 {
			svc.StackContainers = stack
			r.services[id] = svc
		}
	}

	return r
}

func (r *Registry) Get(id string) (model.ServiceTemplate, bool) {
	s, ok := r.services[id]
	return s, ok
}

func (r *Registry) All() []model.ServiceTemplate {
	result := make([]model.ServiceTemplate, 0, len(r.order))
	for _, id := range r.order {
		result = append(result, r.services[id])
	}
	return result
}

// ValidateDependencies checks that all dependencies of a service are running.
func (r *Registry) ValidateDependencies(id string, isRunning func(string) bool) error {
	svc, ok := r.services[id]
	if !ok {
		return fmt.Errorf("unknown service: %s", id)
	}
	for _, dep := range svc.Dependencies {
		if !isRunning(dep) {
			return fmt.Errorf("dependency %q is not running", dep)
		}
	}
	return nil
}

// NewTestRegistry creates a registry with custom templates (for testing).
func NewTestRegistry(tmpls []model.ServiceTemplate) *Registry {
	r := &Registry{
		services: make(map[string]model.ServiceTemplate, len(tmpls)),
	}
	for _, t := range tmpls {
		r.services[t.ID] = t
		r.order = append(r.order, t.ID)
	}
	return r
}

// Dependents returns services that depend on the given service.
func (r *Registry) Dependents(id string) []string {
	var deps []string
	for _, svc := range r.services {
		for _, d := range svc.Dependencies {
			if d == id {
				deps = append(deps, svc.ID)
			}
		}
	}
	return deps
}
