package updates

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sync"
	"syscall"
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
	healthWait time.Duration  // wait before health check (default 30s)
}

func NewEngine(s *store.Store, r *service.Registry, c *docker.ComposeClient) *Engine {
	return &Engine{
		store:      s,
		registry:   r,
		compose:    c,
		stopCh:     make(chan struct{}),
		triggerCh:  make(chan struct{}, 1),
		updating:   make(map[string]bool),
		healthWait: 30 * time.Second,
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
		if tmpl.UpdateSource == nil {
			// Clean up stale checks for services that lost their UpdateSource
			_ = e.store.DeleteUpdateCheck(tmpl.ID)
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
		} else if src.Type == model.SourceDockerDigest {
			// For digest-based checks, use the local image digest directly
			currentVersion = info.Digest
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

// RunPreflight checks whether a service is safe to update and returns detailed results.
func (e *Engine) RunPreflight(serviceID string) (*model.PreflightResult, error) {
	result := &model.PreflightResult{
		ServiceID:  serviceID,
		CanProceed: true,
	}

	// 1. Service exists and has UpdateSource
	tmpl, ok := e.registry.Get(serviceID)
	if !ok {
		result.Checks = append(result.Checks, model.PreflightCheck{
			Name: "service_exists", Status: "fail", Message: "unknown service", Blocking: true,
		})
		result.CanProceed = false
		return result, nil
	}
	if tmpl.UpdateSource == nil {
		result.Checks = append(result.Checks, model.PreflightCheck{
			Name: "update_source", Status: "fail", Message: "service has no update source configured", Blocking: true,
		})
		result.CanProceed = false
		return result, nil
	}
	result.Checks = append(result.Checks, model.PreflightCheck{
		Name: "service_exists", Status: "pass", Message: "service found with update source", Blocking: true,
	})

	// 2. Update available
	check, _ := e.store.GetLatestUpdateCheck(serviceID)
	if check == nil || !check.HasUpdate {
		result.Checks = append(result.Checks, model.PreflightCheck{
			Name: "update_available", Status: "fail", Message: "no update available", Blocking: true,
		})
		result.CanProceed = false
	} else {
		result.FromVersion = check.CurrentVersion
		result.ToVersion = check.LatestVersion
		result.Checks = append(result.Checks, model.PreflightCheck{
			Name: "update_available", Status: "pass",
			Message:  fmt.Sprintf("update available: %s → %s", check.CurrentVersion, check.LatestVersion),
			Blocking: true,
		})
	}

	// 3. Not already updating
	if e.IsUpdating(serviceID) {
		result.Checks = append(result.Checks, model.PreflightCheck{
			Name: "not_updating", Status: "fail", Message: "update already in progress", Blocking: true,
		})
		result.CanProceed = false
	} else {
		result.Checks = append(result.Checks, model.PreflightCheck{
			Name: "not_updating", Status: "pass", Message: "no update in progress", Blocking: true,
		})
	}

	// 4. Compose file accessible
	composePath := tmpl.ComposeDir + "/docker-compose.yml"
	if _, err := os.Stat(composePath); err != nil {
		result.Checks = append(result.Checks, model.PreflightCheck{
			Name: "compose_file", Status: "fail", Message: "compose file not accessible: " + err.Error(), Blocking: true,
		})
		result.CanProceed = false
	} else {
		result.Checks = append(result.Checks, model.PreflightCheck{
			Name: "compose_file", Status: "pass", Message: "compose file accessible", Blocking: true,
		})
	}

	// 5. Disk space (require at least 2GB free)
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/srv/truffels", &stat); err != nil {
		result.Checks = append(result.Checks, model.PreflightCheck{
			Name: "disk_space", Status: "fail", Message: "cannot check disk space: " + err.Error(), Blocking: true,
		})
		result.CanProceed = false
	} else {
		availGB := float64(stat.Bavail*uint64(stat.Bsize)) / (1 << 30)
		if availGB < 2.0 {
			result.Checks = append(result.Checks, model.PreflightCheck{
				Name: "disk_space", Status: "fail",
				Message:  fmt.Sprintf("insufficient disk space: %.1f GB available (need 2 GB)", availGB),
				Blocking: true,
			})
			result.CanProceed = false
		} else {
			result.Checks = append(result.Checks, model.PreflightCheck{
				Name: "disk_space", Status: "pass",
				Message:  fmt.Sprintf("%.1f GB available", availGB),
				Blocking: true,
			})
		}
	}

	// 6. Dependencies healthy
	for _, dep := range tmpl.Dependencies {
		depTmpl, depOK := e.registry.Get(dep)
		if !depOK {
			result.Checks = append(result.Checks, model.PreflightCheck{
				Name: "dependency_" + dep, Status: "fail",
				Message: "dependency not found in registry", Blocking: true,
			})
			result.CanProceed = false
			continue
		}
		allHealthy := true
		for _, cname := range depTmpl.ContainerNames {
			cs, err := docker.InspectContainer(cname)
			if err != nil || cs.Status != "running" {
				allHealthy = false
				break
			}
			if cs.Health == "unhealthy" {
				allHealthy = false
				break
			}
		}
		if !allHealthy {
			result.Checks = append(result.Checks, model.PreflightCheck{
				Name: "dependency_" + dep, Status: "fail",
				Message: dep + " is not healthy", Blocking: true,
			})
			result.CanProceed = false
		} else {
			result.Checks = append(result.Checks, model.PreflightCheck{
				Name: "dependency_" + dep, Status: "pass",
				Message: dep + " is healthy", Blocking: true,
			})
		}
	}

	// 7. Affected dependents (warning only)
	dependents := e.registry.Dependents(serviceID)
	for _, depID := range dependents {
		depTmpl, depOK := e.registry.Get(depID)
		if !depOK {
			continue
		}
		running := false
		for _, cname := range depTmpl.ContainerNames {
			cs, _ := docker.InspectContainer(cname)
			if cs.Status == "running" {
				running = true
				break
			}
		}
		if running {
			result.Checks = append(result.Checks, model.PreflightCheck{
				Name: "dependent_" + depID, Status: "warn",
				Message:  depID + " depends on this service and may be temporarily disrupted",
				Blocking: false,
			})
		}
	}

	return result, nil
}

func (e *Engine) alertUpdateFailed(serviceID, msg string) {
	_ = e.store.UpsertAlert(&model.Alert{
		Type:      "update_failed",
		Severity:  model.SeverityCritical,
		ServiceID: serviceID,
		Message:   msg,
	})
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

	// Snapshot compose file before update
	composePath := tmpl.ComposeDir + "/docker-compose.yml"
	if snapshot, err := os.ReadFile(composePath); err == nil {
		_ = e.store.CreateConfigRevision(&model.ConfigRevision{
			ServiceID:        serviceID,
			Actor:            "update_engine",
			Diff:             fmt.Sprintf("pre-update snapshot (%s → %s)", check.CurrentVersion, check.LatestVersion),
			ConfigSnapshot:   string(snapshot),
			ValidationResult: "ok",
		})
	}

	// Step 1: Pull or build
	if tmpl.FloatingTag {
		// Floating-tag: pull the same tag (gets new image layers)
		_ = e.store.UpdateLogStatus(logID, model.UpdatePulling, "", "")
		for _, img := range src.Images {
			pullRef := img + ":" + src.TagFilter
			if _, err := e.compose.Pull(pullRef); err != nil {
				_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "pull failed: "+err.Error(), "")
				e.alertUpdateFailed(serviceID, "pull failed: "+err.Error())
				return &UpdateError{Msg: "pull failed: " + err.Error()}
			}
		}
		// No compose file rewrite needed — tag stays the same
	} else if src.NeedsBuild {
		_ = e.store.UpdateLogStatus(logID, model.UpdateBuilding, "", "")
		if err := e.compose.Build(serviceID); err != nil {
			_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "build failed: "+err.Error(), "")
			e.alertUpdateFailed(serviceID, "build failed: "+err.Error())
			return &UpdateError{Msg: "build failed: " + err.Error()}
		}
	} else {
		_ = e.store.UpdateLogStatus(logID, model.UpdatePulling, "", "")
		for _, img := range src.Images {
			newImage := img + ":" + check.LatestVersion
			if _, err := e.compose.Pull(newImage); err != nil {
				_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "pull failed: "+err.Error(), "")
				e.alertUpdateFailed(serviceID, "pull failed ("+img+"): "+err.Error())
				return &UpdateError{Msg: "pull failed (" + img + "): " + err.Error()}
			}
		}
	}

	// Step 1b: Update compose file image tags (skip for floating-tag and custom builds)
	if !src.NeedsBuild && !tmpl.FloatingTag {
		composePath := tmpl.ComposeDir + "/docker-compose.yml"
		if err := updateComposeImageTags(composePath, src.Images, check.CurrentVersion, check.LatestVersion); err != nil {
			_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "compose rewrite failed: "+err.Error(), "")
			e.alertUpdateFailed(serviceID, "compose rewrite failed: "+err.Error())
			return &UpdateError{Msg: "compose rewrite failed: " + err.Error()}
		}
	}

	// Step 2: Restart with new image
	_ = e.store.UpdateLogStatus(logID, model.UpdateRestarting, "", check.CurrentVersion)

	if err := e.compose.Down(serviceID); err != nil {
		_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "stop failed: "+err.Error(), check.CurrentVersion)
		e.alertUpdateFailed(serviceID, "stop failed: "+err.Error())
		return &UpdateError{Msg: "stop failed: " + err.Error()}
	}
	if err := e.compose.Up(serviceID); err != nil {
		if tmpl.FloatingTag {
			// No rollback possible for floating tags — old image is overwritten
			_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "start failed (no rollback for floating tag): "+err.Error(), "")
			e.alertUpdateFailed(serviceID, "start failed (no rollback for floating tag): "+err.Error())
			return &UpdateError{Msg: "start failed: " + err.Error()}
		}
		slog.Error("update: start failed, rolling back", "service", serviceID, "err", err)
		e.rollback(serviceID, tmpl, src, check.CurrentVersion, check.LatestVersion)
		_ = e.store.UpdateLogStatus(logID, model.UpdateRolledBack, "start failed: "+err.Error(), check.CurrentVersion)
		e.alertUpdateFailed(serviceID, "start failed, rolled back to "+check.CurrentVersion)
		return &UpdateError{Msg: "start failed, rolled back: " + err.Error()}
	}

	// Step 3: Wait for health check
	time.Sleep(e.healthWait)

	healthy := e.checkHealth(tmpl)
	if !healthy {
		if tmpl.FloatingTag {
			_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "unhealthy after update (no rollback for floating tag)", "")
			e.alertUpdateFailed(serviceID, "unhealthy after update (no rollback for floating tag)")
			return &UpdateError{Msg: "service unhealthy after update, no rollback available for floating tag"}
		}
		slog.Error("update: service unhealthy after update, rolling back", "service", serviceID)
		e.rollback(serviceID, tmpl, src, check.CurrentVersion, check.LatestVersion)
		_ = e.store.UpdateLogStatus(logID, model.UpdateRolledBack, "unhealthy after update", check.CurrentVersion)
		e.alertUpdateFailed(serviceID, "unhealthy after update, rolled back to "+check.CurrentVersion)
		return &UpdateError{Msg: "service unhealthy after update, rolled back"}
	}

	// Success
	_ = e.store.UpdateLogStatus(logID, model.UpdateDone, "", "")
	_ = e.store.ResolveAlerts("update_failed", serviceID)
	slog.Info("update complete", "service", serviceID, "version", check.LatestVersion)

	// Update the check to reflect no pending update
	_ = e.store.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      serviceID,
		CurrentVersion: check.LatestVersion,
		LatestVersion:  check.LatestVersion,
		HasUpdate:      false,
	})

	return nil
}

// RollbackService manually rolls back a service to its previous version.
func (e *Engine) RollbackService(serviceID string) error {
	tmpl, ok := e.registry.Get(serviceID)
	if !ok {
		return &UpdateError{Msg: "unknown service"}
	}
	if tmpl.UpdateSource == nil {
		return &UpdateError{Msg: "service has no update source"}
	}
	if tmpl.FloatingTag {
		return &UpdateError{Msg: "rollback not available for floating-tag services"}
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

	// Find the last successful update to get the previous version
	logs, _ := e.store.GetUpdateLogs(serviceID, 10)
	var prevVersion string
	for _, l := range logs {
		if l.Status == model.UpdateDone {
			prevVersion = l.FromVersion
			break
		}
	}
	if prevVersion == "" {
		return &UpdateError{Msg: "no previous version found to rollback to"}
	}

	check, _ := e.store.GetLatestUpdateCheck(serviceID)
	currentVersion := ""
	if check != nil {
		currentVersion = check.CurrentVersion
	}
	if currentVersion == prevVersion {
		return &UpdateError{Msg: "already at the previous version"}
	}

	src := tmpl.UpdateSource

	log := &model.UpdateLog{
		ServiceID:   serviceID,
		FromVersion: currentVersion,
		ToVersion:   prevVersion,
		Status:      model.UpdatePending,
	}
	logID, err := e.store.CreateUpdateLog(log)
	if err != nil {
		return &UpdateError{Msg: "cannot create rollback log: " + err.Error()}
	}

	// Pull old version
	_ = e.store.UpdateLogStatus(logID, model.UpdatePulling, "", "")
	if src.NeedsBuild {
		_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "rollback not supported for custom-built services", "")
		return &UpdateError{Msg: "rollback not supported for custom-built services"}
	}
	for _, img := range src.Images {
		if _, err := e.compose.Pull(img + ":" + prevVersion); err != nil {
			_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "pull failed: "+err.Error(), "")
			e.alertUpdateFailed(serviceID, "rollback pull failed: "+err.Error())
			return &UpdateError{Msg: "pull failed: " + err.Error()}
		}
	}
	// Rewrite compose tags
	composePath := tmpl.ComposeDir + "/docker-compose.yml"
	if err := updateComposeImageTags(composePath, src.Images, currentVersion, prevVersion); err != nil {
		_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "compose rewrite failed: "+err.Error(), "")
		e.alertUpdateFailed(serviceID, "rollback compose rewrite failed: "+err.Error())
		return &UpdateError{Msg: "compose rewrite failed: " + err.Error()}
	}

	// Restart
	_ = e.store.UpdateLogStatus(logID, model.UpdateRestarting, "", currentVersion)
	_ = e.compose.Down(serviceID)
	if err := e.compose.Up(serviceID); err != nil {
		_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "start failed: "+err.Error(), "")
		e.alertUpdateFailed(serviceID, "rollback start failed: "+err.Error())
		return &UpdateError{Msg: "start failed after rollback: " + err.Error()}
	}

	// Health check
	time.Sleep(e.healthWait)
	if !e.checkHealth(tmpl) {
		_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "unhealthy after rollback", "")
		e.alertUpdateFailed(serviceID, "unhealthy after rollback to "+prevVersion)
		return &UpdateError{Msg: "service unhealthy after rollback"}
	}

	_ = e.store.UpdateLogStatus(logID, model.UpdateDone, "", "")
	_ = e.store.ResolveAlerts("update_failed", serviceID)
	_ = e.store.UpsertUpdateCheck(&model.UpdateCheck{
		ServiceID:      serviceID,
		CurrentVersion: prevVersion,
		LatestVersion:  currentVersion,
		HasUpdate:      true,
	})

	slog.Info("rollback complete", "service", serviceID, "from", currentVersion, "to", prevVersion)
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
			_, _ = e.compose.Pull(img + ":" + currentVersion)
		}
	}
	_ = e.compose.Down(serviceID)
	_ = e.compose.Up(serviceID)
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
