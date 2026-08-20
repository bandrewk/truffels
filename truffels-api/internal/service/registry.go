package service

import (
	"fmt"
	"log/slog"
	"sync"
	"truffels-api/internal/catalog"
	"truffels-api/internal/model"
	"truffels-api/internal/service/templates"
	"truffels-api/internal/store"
)

type CatalogSource interface {
	All() (map[string]catalog.Entry, error)
}

type InstallSource interface {
	ListCatalogInstallations() ([]store.CatalogInstallation, error)
}

type Registry struct {
	mu       sync.RWMutex
	services map[string]model.ServiceTemplate
	order    []string // topological order

	composeRoot   string
	dataRoot      string
	gitHubRepo    string
	catalogSource CatalogSource
	installSource InstallSource

	legacyOrder    []string
	legacyServices map[string]model.ServiceTemplate
}

func NewRegistry(composeRoot, dataRoot, gitHubRepo string, catalogSource CatalogSource, installSource InstallSource) *Registry {
	all := []model.ServiceTemplate{
		templates.Bitcoind,
		templates.Electrs,
		templates.Ckpool,
		templates.Mempool,
		templates.Ckstats,
		templates.Proxy,
		templates.MempoolDB,
		templates.CkstatsDB,
		templates.Truffels,
	}

	r := &Registry{
		composeRoot:    composeRoot,
		dataRoot:       dataRoot,
		gitHubRepo:     gitHubRepo,
		catalogSource:  catalogSource,
		installSource:  installSource,
		legacyServices: make(map[string]model.ServiceTemplate, len(all)),
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
		r.legacyServices[svc.ID] = svc
	}

	// Fixed topological order for the dependency graph
	r.legacyOrder = []string{"bitcoind", "electrs", "ckpool", "mempool-db", "ckstats-db", "mempool", "ckstats", "proxy", "truffels"}

	// Compute stack containers: all containers sharing the same compose dir
	byDir := map[string][]string{}
	for _, svc := range r.legacyServices {
		byDir[svc.ComposeDir] = append(byDir[svc.ComposeDir], svc.ContainerNames...)
	}
	for id, svc := range r.legacyServices {
		stack := byDir[svc.ComposeDir]
		if len(stack) > 1 {
			svc.StackContainers = stack
			r.legacyServices[id] = svc
		}
	}

	// Initialize the current view with legacy services
	r.services = make(map[string]model.ServiceTemplate, len(r.legacyServices))
	for k, v := range r.legacyServices {
		r.services[k] = v
	}
	r.order = make([]string, len(r.legacyOrder))
	copy(r.order, r.legacyOrder)

	return r
}

func (r *Registry) Refresh() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	newServices := make(map[string]model.ServiceTemplate, len(r.legacyServices))
	for k, v := range r.legacyServices {
		newServices[k] = v
	}
	newOrder := make([]string, len(r.legacyOrder))
	copy(newOrder, r.legacyOrder)

	if r.installSource != nil && r.catalogSource != nil {
		installations, err := r.installSource.ListCatalogInstallations()
		if err != nil {
			return fmt.Errorf("failed to list installations: %w", err)
		}
		entries, err := r.catalogSource.All()
		if err != nil {
			return fmt.Errorf("failed to get catalog entries: %w", err)
		}

		for _, inst := range installations {
			entry, ok := entries[inst.CatalogID]
			if !ok {
				slog.Warn("catalog entry not found for installation", "id", inst.CatalogID)
				continue
			}
			tmpl := CatalogEntryToTemplate(entry, r.composeRoot, r.dataRoot)
			newServices[tmpl.ID] = tmpl
			newOrder = append(newOrder, tmpl.ID)
		}
	}

	r.services = newServices
	r.order = newOrder
	return nil
}

func (r *Registry) Get(id string) (model.ServiceTemplate, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.services[id]
	return s, ok
}

func (r *Registry) All() []model.ServiceTemplate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]model.ServiceTemplate, 0, len(r.order))
	for _, id := range r.order {
		result = append(result, r.services[id])
	}
	return result
}

// ValidateDependencies checks that all dependencies of a service are running.
func (r *Registry) ValidateDependencies(id string, isRunning func(string) bool) error {
	r.mu.RLock()
	svc, ok := r.services[id]
	r.mu.RUnlock()

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
	r.mu.RLock()
	defer r.mu.RUnlock()
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
