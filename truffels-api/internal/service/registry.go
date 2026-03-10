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

func NewRegistry(composeRoot string) *Registry {
	all := []model.ServiceTemplate{
		templates.Bitcoind,
		templates.Electrs,
		templates.Ckpool,
		templates.Mempool,
		templates.Ckstats,
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
		r.services[svc.ID] = svc
	}

	// Fixed topological order for the dependency graph
	r.order = []string{"bitcoind", "electrs", "ckpool", "mempool", "ckstats"}

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
