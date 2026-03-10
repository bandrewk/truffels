package updates

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sync"
	"time"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

type Engine struct {
	store    *store.Store
	registry *service.Registry
	compose  *docker.ComposeClient
	stopCh   chan struct{}
	triggerCh chan struct{}
	mu       sync.Mutex
	updating map[string]bool // services currently being updated
}

func NewEngine(s *store.Store, r *service.Registry, c *docker.ComposeClient) *Engine {
	return &Engine{
		store:     s,
		registry:  r,
		compose:   c,
		stopCh:    make(chan struct{}),
		triggerCh: make(chan struct{}, 1),
		updating:  make(map[string]bool),
	}
}

func (e *Engine) Start() {
	go e.loop()
}

func (e *Engine) Stop() {
	close(e.stopCh)
}

// TriggerCheck requests an immediate check cycle.
func (e *Engine) TriggerCheck() {
	select {
	case e.triggerCh <- struct{}{}:
	default:
	}
}

// IsUpdating returns true if a service is currently being updated.
func (e *Engine) IsUpdating(serviceID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.updating[serviceID]
}

func (e *Engine) loop() {
	// Initial check after 30s (give services time to start)
	time.Sleep(30 * time.Second)
	e.checkAll()

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.checkAll()
		case <-e.triggerCh:
			e.checkAll()
		case <-e.stopCh:
			return
		}
	}
}

func (e *Engine) checkAll() {
	slog.Info("update check starting")
	for _, tmpl := range e.registry.All() {
		if tmpl.UpdateSource == nil || tmpl.ReadOnly {
			continue
		}
		e.checkService(tmpl)
	}
	slog.Info("update check complete")
}

func (e *Engine) checkService(tmpl model.ServiceTemplate) {
	src := tmpl.UpdateSource

	// Get current version from running container
	currentVersion := ""
	if len(tmpl.ContainerNames) > 0 {
		info, err := e.compose.ImageInspect(tmpl.ContainerNames[0])
		if err != nil {
			slog.Warn("update check: cannot inspect image", "service", tmpl.ID, "err", err)
		} else {
			currentVersion = ExtractCurrentVersion(src, info.Image)
		}
	}

	// For commit-based sources, use stored version if we can't derive it
	if currentVersion == "" && (src.Type == model.SourceGitHub || src.Type == model.SourceBitbucket) {
		prev, _ := e.store.GetLatestUpdateCheck(tmpl.ID)
		if prev != nil && prev.CurrentVersion != "" {
			currentVersion = prev.CurrentVersion
		}
	}

	// Check latest upstream version
	latestVersion, err := CheckLatestVersion(src)

	check := &model.UpdateCheck{
		ServiceID:      tmpl.ID,
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
	}

	if err != nil {
		check.Error = err.Error()
		slog.Warn("update check failed", "service", tmpl.ID, "err", err)
	} else {
		// For commit-based sources: first check initializes current to latest (no update)
		if currentVersion == "" && latestVersion != "" &&
			(src.Type == model.SourceGitHub || src.Type == model.SourceBitbucket) {
			check.CurrentVersion = latestVersion
			currentVersion = latestVersion
		}
		check.HasUpdate = currentVersion != "" && latestVersion != "" && currentVersion != latestVersion
		if check.HasUpdate {
			slog.Info("update available", "service", tmpl.ID, "current", currentVersion, "latest", latestVersion)
		}
	}

	if err := e.store.UpsertUpdateCheck(check); err != nil {
		slog.Error("store update check", "err", err)
	}
}

// ApplyUpdate performs the update for a single service with automatic rollback on health failure.
func (e *Engine) ApplyUpdate(serviceID string) error {
	tmpl, ok := e.registry.Get(serviceID)
	if !ok {
		return &UpdateError{Msg: "unknown service"}
	}
	if tmpl.UpdateSource == nil {
		return &UpdateError{Msg: "service has no update source"}
	}

	e.mu.Lock()
	if e.updating[serviceID] {
		e.mu.Unlock()
		return &UpdateError{Msg: "update already in progress"}
	}
	e.updating[serviceID] = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.updating, serviceID)
		e.mu.Unlock()
	}()

	check, _ := e.store.GetLatestUpdateCheck(serviceID)
	if check == nil || !check.HasUpdate {
		return &UpdateError{Msg: "no update available"}
	}

	log := &model.UpdateLog{
		ServiceID:   serviceID,
		FromVersion: check.CurrentVersion,
		ToVersion:   check.LatestVersion,
		Status:      model.UpdatePending,
	}
	logID, err := e.store.CreateUpdateLog(log)
	if err != nil {
		return &UpdateError{Msg: "cannot create update log: " + err.Error()}
	}

	src := tmpl.UpdateSource

	// Step 1: Pull or build
	if src.NeedsBuild {
		e.store.UpdateLogStatus(logID, model.UpdateBuilding, "", "")
		if err := e.compose.Build(serviceID); err != nil {
			e.store.UpdateLogStatus(logID, model.UpdateFailed, "build failed: "+err.Error(), "")
			return &UpdateError{Msg: "build failed: " + err.Error()}
		}
	} else {
		e.store.UpdateLogStatus(logID, model.UpdatePulling, "", "")
		for _, img := range src.Images {
			newImage := img + ":" + check.LatestVersion
			if err := e.compose.Pull(newImage); err != nil {
				e.store.UpdateLogStatus(logID, model.UpdateFailed, "pull failed: "+err.Error(), "")
				return &UpdateError{Msg: "pull failed (" + img + "): " + err.Error()}
			}
		}
	}

	// Step 1b: Update compose file image tags
	if !src.NeedsBuild {
		composePath := tmpl.ComposeDir + "/docker-compose.yml"
		if err := updateComposeImageTags(composePath, src.Images, check.CurrentVersion, check.LatestVersion); err != nil {
			e.store.UpdateLogStatus(logID, model.UpdateFailed, "compose rewrite failed: "+err.Error(), "")
			return &UpdateError{Msg: "compose rewrite failed: " + err.Error()}
		}
	}

	// Step 2: Restart with new image
	e.store.UpdateLogStatus(logID, model.UpdateRestarting, "", check.CurrentVersion)

	if err := e.compose.Down(serviceID); err != nil {
		e.store.UpdateLogStatus(logID, model.UpdateFailed, "stop failed: "+err.Error(), check.CurrentVersion)
		return &UpdateError{Msg: "stop failed: " + err.Error()}
	}
	if err := e.compose.Up(serviceID); err != nil {
		// Attempt rollback
		slog.Error("update: start failed, rolling back", "service", serviceID, "err", err)
		e.rollback(serviceID, tmpl, src, check.CurrentVersion, check.LatestVersion)
		e.store.UpdateLogStatus(logID, model.UpdateRolledBack, "start failed: "+err.Error(), check.CurrentVersion)
		return &UpdateError{Msg: "start failed, rolled back: " + err.Error()}
	}

	// Step 3: Wait for health check (30s)
	time.Sleep(30 * time.Second)

	healthy := e.checkHealth(tmpl)
	if !healthy {
		slog.Error("update: service unhealthy after update, rolling back", "service", serviceID)
		e.rollback(serviceID, tmpl, src, check.CurrentVersion, check.LatestVersion)
		e.store.UpdateLogStatus(logID, model.UpdateRolledBack, "unhealthy after update", check.CurrentVersion)
		return &UpdateError{Msg: "service unhealthy after update, rolled back"}
	}

	// Success
	e.store.UpdateLogStatus(logID, model.UpdateDone, "", "")
	slog.Info("update complete", "service", serviceID, "version", check.LatestVersion)

	// Update the check to reflect no pending update
	e.store.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      serviceID,
		CurrentVersion: check.LatestVersion,
		LatestVersion:  check.LatestVersion,
		HasUpdate:      false,
	})

	return nil
}

func (e *Engine) rollback(serviceID string, tmpl model.ServiceTemplate, src *model.UpdateSource, currentVersion, newVersion string) {
	if !src.NeedsBuild {
		// Revert compose file to old version
		composePath := tmpl.ComposeDir + "/docker-compose.yml"
		if err := updateComposeImageTags(composePath, src.Images, newVersion, currentVersion); err != nil {
			slog.Error("rollback: compose rewrite failed", "service", serviceID, "err", err)
		}
		for _, img := range src.Images {
			e.compose.Pull(img + ":" + currentVersion)
		}
	}
	e.compose.Down(serviceID)
	e.compose.Up(serviceID)
}

func (e *Engine) checkHealth(tmpl model.ServiceTemplate) bool {
	for _, name := range tmpl.ContainerNames {
		cs, err := docker.InspectContainer(name)
		if err != nil {
			return false
		}
		if cs.Status != "running" {
			return false
		}
		// If the container has a health check and it's unhealthy, fail
		if cs.Health == "unhealthy" {
			return false
		}
	}
	return true
}

// updateComposeImageTags rewrites image tags in a docker-compose.yml file.
// For each image in images, it replaces "image: <name>:<oldTag>..." with "image: <name>:<newTag>".
// This handles digest-pinned images (e.g. "image: mempool/backend:v3.2.0@sha256:...").
func updateComposeImageTags(composePath string, images []string, oldTag, newTag string) error {
	data, err := os.ReadFile(composePath)
	if err != nil {
		return fmt.Errorf("read compose file: %w", err)
	}

	content := string(data)
	for _, img := range images {
		// Match "image: <img>:<oldTag>" optionally followed by "@sha256:..."
		pattern := fmt.Sprintf(`(image:\s*)%s:%s(@sha256:[a-f0-9]+)?`, regexp.QuoteMeta(img), regexp.QuoteMeta(oldTag))
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("compile regex for %s: %w", img, err)
		}
		replacement := fmt.Sprintf("${1}%s:%s", img, newTag)
		content = re.ReplaceAllString(content, replacement)
	}

	if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write compose file: %w", err)
	}
	slog.Info("compose file updated", "path", composePath, "images", images, "version", newTag)
	return nil
}

type UpdateError struct {
	Msg string
}

func (e *UpdateError) Error() string {
	return e.Msg
}
