package updates

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

type Engine struct {
	store      *store.Store
	registry   *service.Registry
	compose    *docker.ComposeClient
	stopCh     chan struct{}
	triggerCh  chan struct{}
	mu         sync.Mutex
	updating   map[string]bool // services currently being updated
	healthWait time.Duration   // wait before health check (default 30s)
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
	e.reconcileStuckUpdates()
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

func (e *Engine) isCheckEnabled() bool {
	val, err := e.store.GetSetting("update_check_enabled")
	if err != nil || val == "" {
		return true // default enabled
	}
	return val == "true"
}

func (e *Engine) getUpdateChannel() string {
	val, err := e.store.GetSetting("update_channel")
	if err != nil || val == "" {
		return "stable"
	}
	return val
}

func (e *Engine) getCheckInterval() time.Duration {
	val, err := e.store.GetSetting("update_check_interval_hours")
	if err == nil && val != "" {
		if hours, err := strconv.Atoi(val); err == nil && hours >= 1 && hours <= 168 {
			return time.Duration(hours) * time.Hour
		}
	}
	return 24 * time.Hour
}

func (e *Engine) loop() {
	// Initial check after 30s (give services time to start)
	time.Sleep(30 * time.Second)
	if e.isCheckEnabled() {
		e.checkAll()
	}

	interval := e.getCheckInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if e.isCheckEnabled() {
				e.checkAll()
			}
			// Re-read interval in case it changed
			newInterval := e.getCheckInterval()
			if newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
				slog.Info("update check interval changed", "hours", int(interval.Hours()))
			}
		case <-e.triggerCh:
			// Manual trigger always runs regardless of enabled setting
			e.checkAll()
		case <-e.stopCh:
			return
		}
	}
}

func (e *Engine) checkAll() {
	slog.Info("update check starting")
	registered := make(map[string]bool)
	for _, tmpl := range e.registry.All() {
		registered[tmpl.ID] = true
		if tmpl.UpdateSource == nil {
			// Clean up stale checks and alerts for services that lost their UpdateSource
			_ = e.store.DeleteUpdateCheck(tmpl.ID)
			_ = e.store.ResolveAlerts("update_failed", tmpl.ID)
			continue
		}
		e.checkService(tmpl)
	}
	// Purge update_checks for service IDs no longer in the registry (e.g. after consolidation)
	if checks, err := e.store.GetAllUpdateChecks(); err == nil {
		for _, c := range checks {
			if !registered[c.ServiceID] {
				slog.Info("purging stale update check", "service", c.ServiceID)
				_ = e.store.DeleteUpdateCheck(c.ServiceID)
			}
		}
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
			// Labels first: for the services we build ourselves the tag is only a
			// claim the update path tries to keep true (it retags the compose
			// file on every build, best-effort), while the ref stamped into the
			// image at build time is the proof. Everything else still reads the
			// tag.
			currentVersion = ExtractCurrentVersionFromLabels(src, info.Image, info.Labels)
		}
	}

	// For a service we build ourselves from a git source, the source-ref label is
	// the only authority on what is running: the compose tag is written by us and
	// can lag the image behind it. No label means the running version is unknown, and an
	// unknown version must not be papered over — not with the stored row (which
	// the initialisation below used to poison), and not with latestVersion.
	labelIsAuthority := src.NeedsBuild && (src.Type == model.SourceGitHub || src.Type == model.SourceBitbucket)

	// For commit-based sources, use stored version if we can't derive it
	if currentVersion == "" && !labelIsAuthority &&
		(src.Type == model.SourceGitHub || src.Type == model.SourceBitbucket) {
		prev, _ := e.store.GetLatestUpdateCheck(tmpl.ID)
		if prev != nil && prev.CurrentVersion != "" {
			currentVersion = prev.CurrentVersion
		}
	}

	// For Docker Hub / GitHub Release sources, fall back to last-known version when container is stopped
	if currentVersion == "" && (src.Type == model.SourceDockerHub || src.Type == model.SourceGitHubRelease) {
		prev, _ := e.store.GetLatestUpdateCheck(tmpl.ID)
		if prev != nil && prev.CurrentVersion != "" {
			currentVersion = prev.CurrentVersion
		}
	}

	// Container doesn't exist and no stored version — skip (e.g. pruned mode)
	if currentVersion == "" && src.Type != model.SourceGitHub && src.Type != model.SourceBitbucket {
		_ = e.store.DeleteUpdateCheck(tmpl.ID)
		return
	}

	// Check latest upstream version
	latestVersion, err := CheckLatestVersion(src, e.getUpdateChannel())

	check := &model.UpdateCheck{
		ServiceID:      tmpl.ID,
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
	}

	if err != nil {
		check.Error = err.Error()
		slog.Warn("update check failed", "service", tmpl.ID, "err", err)
	} else if labelIsAuthority && currentVersion == "" {
		// Unknown running version. Offering the rebuild is the only way out: it
		// stamps the label and the next check reads a real version. Reporting
		// "up to date" instead was a dead end — the label needs an update, and
		// the update was never offered because everything looked current.
		//
		// CurrentVersion stays empty on purpose. It becomes prevVersion in
		// RollbackService and FromVersion in the update log, so a stand-in like
		// "unknown" would be recorded as a version that exists and could be
		// rolled back to. Empty is the honest answer.
		check.HasUpdate = latestVersion != ""
		if check.HasUpdate {
			slog.Info("update offered: running build carries no source ref",
				"service", tmpl.ID, "latest", latestVersion)
		}
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

// versionLabel renders a version for a human-readable message. An empty
// version is a deliberate statement — checkService leaves it empty when the
// running image carries no source ref — so name that state instead of letting
// the sentence break off mid-clause ("rolled back to " with nothing after it,
// or "pre-update snapshot ( → v1.2.0)").
func versionLabel(v string) string {
	if v == "" {
		return "an unidentified build (no source ref)"
	}
	return v
}

// statfs is syscall.Statfs behind a package variable so the preflight disk-space
// check can be exercised in both directions. It reads a fixed path under
// /srv/truffels, which does not exist in a test container, so without the seam
// only the "cannot check disk space" arm is ever reached. Production never
// replaces it.
var statfs = syscall.Statfs

// A preflight check is only ever recorded in one of three shapes, and one of
// them carries an obligation: a blocking failure has to clear CanProceed too, or
// the API offers to start an update its own checks just rejected. These three
// helpers make that pairing structural instead of a line every new check has to
// remember to repeat.
func preflightPass(r *model.PreflightResult, name, msg string) {
	r.Checks = append(r.Checks, model.PreflightCheck{Name: name, Status: "pass", Message: msg, Blocking: true})
}

func preflightFail(r *model.PreflightResult, name, msg string) {
	r.Checks = append(r.Checks, model.PreflightCheck{Name: name, Status: "fail", Message: msg, Blocking: true})
	r.CanProceed = false
}

// preflightWarn records what the user should know before starting but that is
// not a fault — a dependent that will be disrupted, not a reason to refuse.
func preflightWarn(r *model.PreflightResult, name, msg string) {
	r.Checks = append(r.Checks, model.PreflightCheck{Name: name, Status: "warn", Message: msg, Blocking: false})
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
		preflightFail(result, "service_exists", "unknown service")
		return result, nil
	}
	if tmpl.UpdateSource == nil {
		preflightFail(result, "update_source", "service has no update source configured")
		return result, nil
	}
	preflightPass(result, "service_exists", "service found with update source")

	// 2. Update available
	check, _ := e.store.GetLatestUpdateCheck(serviceID)
	if check == nil || !check.HasUpdate {
		preflightFail(result, "update_available", "no update available")
	} else {
		result.FromVersion = check.CurrentVersion
		result.ToVersion = check.LatestVersion
		// An empty CurrentVersion is a deliberate statement, not a gap:
		// checkService leaves it empty when the running image carries no
		// source-ref label. Say so, rather than rendering it as a blank on the
		// left of the arrow ("update available:  → 8f2e7c2f"), which reads like
		// a formatting bug and tells the user nothing about why.
		msg := fmt.Sprintf("update available: %s → %s", check.CurrentVersion, check.LatestVersion)
		if check.CurrentVersion == "" {
			msg = fmt.Sprintf("update available: running version unknown (the image carries no source ref) → %s; rebuilding stamps it", check.LatestVersion)
		}
		preflightPass(result, "update_available", msg)
	}

	// 3. Not already updating
	if e.IsUpdating(serviceID) {
		preflightFail(result, "not_updating", "update already in progress")
	} else {
		preflightPass(result, "not_updating", "no update in progress")
	}

	// 4. Compose file accessible
	composePath := tmpl.ComposeDir + "/docker-compose.yml"
	if _, err := os.Stat(composePath); err != nil {
		preflightFail(result, "compose_file", "compose file not accessible: "+err.Error())
	} else {
		preflightPass(result, "compose_file", "compose file accessible")
	}

	// 5. Disk space (require at least 2GB free)
	var stat syscall.Statfs_t
	if err := statfs("/srv/truffels", &stat); err != nil {
		preflightFail(result, "disk_space", "cannot check disk space: "+err.Error())
	} else {
		availGB := float64(stat.Bavail*uint64(stat.Bsize)) / (1 << 30)
		if availGB < 2.0 {
			preflightFail(result, "disk_space", fmt.Sprintf("insufficient disk space: %.1f GB available (need 2 GB)", availGB))
		} else {
			preflightPass(result, "disk_space", fmt.Sprintf("%.1f GB available", availGB))
		}
	}

	// 6. Dependencies healthy
	for _, dep := range tmpl.Dependencies {
		depTmpl, depOK := e.registry.Get(dep)
		if !depOK {
			preflightFail(result, "dependency_"+dep, "dependency not found in registry")
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
			preflightFail(result, "dependency_"+dep, dep+" is not healthy")
		} else {
			preflightPass(result, "dependency_"+dep, dep+" is healthy")
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
			preflightWarn(result, "dependent_"+depID, depID+" depends on this service and may be temporarily disrupted")
		}
	}

	return result, nil
}

// ListAvailableVersions returns the list of versions the user can pick from
// for this service. Empty list for source types that aren't version-selectable
// (GitHub commit SHAs, BitbucketSourceBuild, GitHubRelease, DockerDigest).
func (e *Engine) ListAvailableVersions(serviceID string) ([]string, error) {
	tmpl, ok := e.registry.Get(serviceID)
	if !ok {
		return nil, fmt.Errorf("unknown service: %s", serviceID)
	}
	if tmpl.UpdateSource == nil {
		return nil, nil
	}
	if tmpl.UpdateSource.Type != model.SourceDockerHub {
		return nil, nil
	}
	if len(tmpl.UpdateSource.Images) == 0 {
		return nil, nil
	}
	return ListDockerHubVersions(tmpl.UpdateSource.Images[0], tmpl.UpdateSource.TagFilter)
}

func (e *Engine) alertUpdateFailed(serviceID, msg string) {
	_ = e.store.UpsertAlert(&model.Alert{
		Type:      "update_failed",
		Severity:  model.SeverityCritical,
		ServiceID: serviceID,
		Message:   msg,
	})
}

// failUpdate ends a failed update step: it records msg on the update log, raises
// the update_failed alert and hands msg back as the error. The three belong
// together — a step that logs without alerting leaves the UI quiet about a
// service that is down — and writing them as one call is what makes forgetting
// one impossible. rollbackVersion is the version the service can still be
// returned to; empty when the step failed before anything was staged.
//
// Not every failure fits this shape, and the ones that do not are left written
// out on purpose. Several paths word the log, the alert and the returned error
// differently — a failed pull during a rollback logs "pull failed" but alerts
// "rollback pull failed", so the alert names the flow that broke — and folding
// them in would mean a helper taking three separate strings, which reads worse
// than the lines it replaces. One path, the github_release refusal in
// RollbackService, deliberately raises no alert at all.
func (e *Engine) failUpdate(logID int64, serviceID, msg, rollbackVersion string) error {
	_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, msg, rollbackVersion)
	e.alertUpdateFailed(serviceID, msg)
	return &UpdateError{Msg: msg}
}

// ApplyUpdate performs the update for a single service with automatic rollback on health failure.
// ApplyUpdate applies the auto-detected latest version.
// Equivalent to ApplyUpdateToVersion(serviceID, "").
func (e *Engine) ApplyUpdate(serviceID string) error {
	return e.ApplyUpdateToVersion(serviceID, "")
}

// ApplyUpdateToVersion applies a specific version. If targetVersion is empty,
// uses the auto-detected check.LatestVersion (unchanged dev.16 behavior).
// Otherwise overrides check.LatestVersion with the user-selected version
// — this allows the UI's version-selector dropdown to drive the update.
func (e *Engine) ApplyUpdateToVersion(serviceID, targetVersion string) error {
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
	if check == nil {
		return &UpdateError{Msg: "no update check available"}
	}
	if targetVersion != "" {
		check.LatestVersion = targetVersion
		check.HasUpdate = true
	} else if !check.HasUpdate {
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

	// For github_release sources (truffels self-update), use a dedicated flow.
	if src.Type == model.SourceGitHubRelease {
		return e.applySelfUpdate(serviceID, tmpl, check, logID)
	}

	// Snapshot compose file before update
	composePath := tmpl.ComposeDir + "/docker-compose.yml"
	if snapshot, err := os.ReadFile(composePath); err == nil {
		_ = e.store.CreateConfigRevision(&model.ConfigRevision{
			ServiceID:        serviceID,
			Actor:            "update_engine",
			Diff:             fmt.Sprintf("pre-update snapshot (%s → %s)", versionLabel(check.CurrentVersion), versionLabel(check.LatestVersion)),
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
				return e.failUpdate(logID, serviceID, "pull failed: "+err.Error(), "")
			}
		}
		// No compose file rewrite needed — tag stays the same
	} else if src.NeedsBuild {
		if err := e.applyNeedsBuild(serviceID, tmpl, src, check, logID); err != nil {
			return err
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

	// Step 1b: Update compose file image tags.
	//
	// Floating tags stay put by definition — the tag is the target. Custom
	// builds are rewritten too, but inside applyNeedsBuild and *before* the
	// build: the build tags its output with what the file says, so a rewrite
	// here would name a tag no image carries. Their rewrite is not repeated
	// here.
	if !src.NeedsBuild && !tmpl.FloatingTag {
		if err := e.compose.RewriteTags(serviceID, src.Images, check.CurrentVersion, check.LatestVersion); err != nil {
			return e.failUpdate(logID, serviceID, "compose rewrite failed: "+err.Error(), "")
		}
	}

	// Step 2: Restart with new image
	_ = e.store.UpdateLogStatus(logID, model.UpdateRestarting, "", check.CurrentVersion)

	if err := e.compose.Down(serviceID); err != nil {
		return e.failUpdate(logID, serviceID, "stop failed: "+err.Error(), check.CurrentVersion)
	}
	if err := e.compose.Up(serviceID); err != nil {
		if tmpl.FloatingTag {
			// No rollback possible for floating tags — old image is overwritten
			_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "start failed (no rollback for floating tag): "+err.Error(), "")
			e.alertUpdateFailed(serviceID, "start failed (no rollback for floating tag): "+err.Error())
			return &UpdateError{Msg: "start failed: " + err.Error()}
		}
		slog.Error("update: start failed, rolling back", "service", serviceID, "err", err)
		if rbErr := e.rollback(serviceID, tmpl, src, check.CurrentVersion, check.LatestVersion); rbErr != nil {
			return e.failUpdate(logID, serviceID,
				"start failed: "+err.Error()+"; rollback incomplete: "+rbErr.Error(), check.CurrentVersion)
		}
		_ = e.store.UpdateLogStatus(logID, model.UpdateRolledBack, "start failed: "+err.Error(), check.CurrentVersion)
		e.alertUpdateFailed(serviceID, "start failed, rolled back to "+versionLabel(check.CurrentVersion))
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
		if rbErr := e.rollback(serviceID, tmpl, src, check.CurrentVersion, check.LatestVersion); rbErr != nil {
			return e.failUpdate(logID, serviceID,
				"unhealthy after update; rollback incomplete: "+rbErr.Error(), check.CurrentVersion)
		}
		_ = e.store.UpdateLogStatus(logID, model.UpdateRolledBack, "unhealthy after update", check.CurrentVersion)
		e.alertUpdateFailed(serviceID, "unhealthy after update, rolled back to "+versionLabel(check.CurrentVersion))
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

	// Prune old images in the background
	go e.pruneOldImages(serviceID, src)

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
	// A rollback needs a known starting point on both ends. An empty
	// CurrentVersion is checkService's deliberate "this build carries no source
	// ref, so we cannot prove what runs" — restoring one unidentified image over
	// another and reporting the version it supposedly went back to is exactly
	// the lie the source-ref check exists to end. The UI hides the button in
	// this state; this is the guard for everyone who calls the endpoint directly.
	if currentVersion == "" {
		return &UpdateError{Msg: "cannot roll back: the running version is unknown (the image carries no source ref) — rebuild first so it can be identified"}
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

	// Restore the old image
	_ = e.store.UpdateLogStatus(logID, model.UpdatePulling, "", "")
	if src.NeedsBuild {
		// The truffels stack is NeedsBuild as well, but it is a github_release:
		// ApplyUpdateToVersion routes it to applySelfUpdate before it ever gets
		// here, and nothing is staged under a truffels/truffels:rollback tag.
		// Without this guard the retag path below chases an image that does not
		// exist and ends in a misleading "no rollback image available" plus an
		// alert. Keep the honest message this path had before retag rollbacks.
		//
		// Deliberately not failUpdate: nothing was started, so nothing is broken,
		// and an update_failed alert would report a fault that does not exist.
		// TestRollbackService_SelfUpdateIsRefusedHonestly asserts the absence.
		if src.Type == model.SourceGitHubRelease {
			_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "rollback not supported for custom-built services", "")
			return &UpdateError{Msg: "rollback not supported for custom-built services"}
		}

		// Nothing to pull — a custom build exists only on this box. The image
		// running before the last update was staged under :rollback; move that
		// tag back onto the ref the compose file runs. Fail loudly if it is
		// gone: restarting the current image and calling it a rollback is the
		// bug this replaces.
		//
		// Check the staged image *before* moving any tag: :rollback holds
		// exactly one generation and is only ever staged by an update, never by
		// a rollback. After one successful rollback it points at the image that
		// is already running, so a second rollback would retag a no-op, come up
		// healthy and then record prevVersion as the running version — the
		// running code would be one generation older than the UI claims.
		// Verifying first means a refusal leaves nothing half-done.
		rollbackRef := rollbackImageRef(serviceID)
		info, inspectErr := e.compose.ImageInspectByName(rollbackRef)
		if inspectErr != nil {
			return e.failUpdate(logID, serviceID, "cannot verify the staged rollback image: "+inspectErr.Error(), "")
		}
		staged := info.Labels[SourceRefLabel]
		switch {
		case isPlaceholderRef(staged):
			// Either nothing is staged (the agent answers 200 with empty fields
			// for an unknown image), or the staged image predates source-ref
			// labels, or it was built with the old ARG SOURCE_REF=unknown
			// default. All three mean we cannot prove what it would restore —
			// and the last one is the dangerous shape: a device installed under
			// dev.24 also has "unknown" in update_log.from_version, so a raw
			// comparison found staged == prevVersion and restored an
			// unidentifiable image while reporting the version it went back to.
			return e.failUpdate(logID, serviceID, fmt.Sprintf(
				"no rollback image available: %s carries no source ref, so it cannot be shown to hold %s", rollbackRef, prevVersion), "")
		case staged != prevVersion:
			return e.failUpdate(logID, serviceID, fmt.Sprintf(
				"staged rollback image holds ref %q, not %q — only one generation is kept, so this version is gone", staged, prevVersion), "")
		}

		// The update that got us here moved the compose tag to the version we
		// are leaving, so the file names the wrong version for the image we are
		// about to restore. Put the image on the tag prevVersion *will* be named
		// by, then move the file — in that order, because a retag that fails
		// leaves the compose file untouched, while a compose file pointed at a
		// tag no image carries would break the next restart.
		live, refFromFile := composeImageRef(tmpl, serviceID)
		repo, liveTag := splitImageRef(live)
		target := live
		if refFromFile && len(src.Images) > 0 && repo != "" && liveTag != prevVersion {
			target = repo + ":" + prevVersion
		}
		if err := e.compose.ImageTag(rollbackRef, target); err != nil {
			_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "no rollback image available: "+err.Error(), "")
			e.alertUpdateFailed(serviceID, "rollback image restore failed: "+err.Error())
			return &UpdateError{Msg: "no rollback image available: " + err.Error()}
		}
		if target != live {
			// Fatal: without the rewrite the file still names the version we are
			// rolling away from, and Down/Up below would restart exactly that
			// image while this reported a rollback.
			if err := e.compose.RewriteTags(serviceID, src.Images, liveTag, prevVersion); err != nil {
				msg := "compose rewrite failed: " + err.Error()
				_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, msg, "")
				e.alertUpdateFailed(serviceID, "rollback compose rewrite failed: "+err.Error())
				return &UpdateError{Msg: msg}
			}
		}
	} else {
		for _, img := range src.Images {
			if _, err := e.compose.Pull(img + ":" + prevVersion); err != nil {
				_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "pull failed: "+err.Error(), "")
				e.alertUpdateFailed(serviceID, "rollback pull failed: "+err.Error())
				return &UpdateError{Msg: "pull failed: " + err.Error()}
			}
		}
		// Rewrite compose tags
		if err := e.compose.RewriteTags(serviceID, src.Images, currentVersion, prevVersion); err != nil {
			_ = e.store.UpdateLogStatus(logID, model.UpdateFailed, "compose rewrite failed: "+err.Error(), "")
			e.alertUpdateFailed(serviceID, "rollback compose rewrite failed: "+err.Error())
			return &UpdateError{Msg: "compose rewrite failed: " + err.Error()}
		}
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
		// Never advertise an update without a target: the UI would render
		// "update available" with nothing to go to, and its button would call
		// ApplyUpdate with an empty SOURCE_REF. The guard above already rules
		// out an empty currentVersion; this keeps the invariant local to the
		// write, where it can be read off the row itself.
		HasUpdate: currentVersion != "" && currentVersion != prevVersion,
	})

	slog.Info("rollback complete", "service", serviceID, "from", currentVersion, "to", prevVersion)
	return nil
}

// rollback returns the service to the state it was in before the update.
// It reports an error when it could not do so — the caller must not claim a
// rollback that did not happen.
func (e *Engine) rollback(serviceID string, tmpl model.ServiceTemplate, src *model.UpdateSource, currentVersion, newVersion string) error {
	var rollbackErr error
	if src.NeedsBuild {
		// A custom build has no registry to pull the old version back from. The
		// previous image was staged under :rollback before the build overwrote
		// the live tag; move it back, otherwise Down/Up would simply restart the
		// very image that just failed while we reported a successful rollback.
		//
		// applyNeedsBuild also moved the compose tag to newVersion, so the ref
		// the file names has to go back to currentVersion as well — restore the
		// image onto that ref first, then move the file, so a failed retag never
		// leaves the compose pointing at a tag no image carries. An unknown
		// currentVersion has no tag to go back to; then the ref stays as it is
		// and only the image is restored, exactly as before.
		live, refFromFile := composeImageRef(tmpl, serviceID)
		repo, liveTag := splitImageRef(live)
		target := live
		if refFromFile && len(src.Images) > 0 && repo != "" && currentVersion != "" && liveTag != currentVersion {
			target = repo + ":" + currentVersion
		}
		if err := e.compose.ImageTag(rollbackImageRef(serviceID), target); err != nil {
			slog.Error("rollback: retag failed", "service", serviceID, "image", target, "err", err)
			rollbackErr = fmt.Errorf("restoring previous image failed: %w", err)
		} else if target != live {
			if err := e.compose.RewriteTags(serviceID, src.Images, liveTag, currentVersion); err != nil {
				// Down/Up below would restart the failed build under its own
				// name. The caller must not report this as a rollback.
				slog.Error("rollback: compose rewrite failed", "service", serviceID, "err", err)
				rollbackErr = fmt.Errorf("restoring the previous compose tag failed: %w", err)
			}
		}
	} else {
		// Revert compose file to old version
		if err := e.compose.RewriteTags(serviceID, src.Images, newVersion, currentVersion); err != nil {
			slog.Error("rollback: compose rewrite failed", "service", serviceID, "err", err)
		}
		for _, img := range src.Images {
			_, _ = e.compose.Pull(img + ":" + currentVersion)
		}
	}
	_ = e.compose.Down(serviceID)
	_ = e.compose.Up(serviceID)
	return rollbackErr
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

// applyNeedsBuild rebuilds a custom-built service at a specific upstream ref
// and verifies the result before starting it. The ref travels into the build as
// the SOURCE_REF build arg and comes back out of the image's source-ref label,
// so a build that silently produced the old code can no longer be reported as a
// successful update — which is exactly what ckpool and ckstats used to do.
// composeImageRe matches the image line a compose file uses for a service's own
// truffels image, e.g. "  image: truffels/ckpool:v1.0.0".
func composeImageRe(serviceID string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*image:\s*(truffels/` + regexp.QuoteMeta(serviceID) + `:[A-Za-z0-9._-]+)\s*$`)
}

// splitImageRef splits an image reference into repository and tag:
// "truffels/ckpool:v1.0.0" -> ("truffels/ckpool", "v1.0.0"). A ref carrying no
// tag returns an empty tag, and a colon belonging to a registry host:port (one
// that appears before the last "/") is not read as a tag separator.
func splitImageRef(ref string) (repo, tag string) {
	idx := strings.LastIndex(ref, ":")
	if idx < 0 || idx < strings.LastIndex(ref, "/") {
		return ref, ""
	}
	return ref[:idx], ref[idx+1:]
}

// liveImageRef is the image reference a custom-built service actually runs —
// the single source of truth for it is the compose file, because that is what
// `docker compose build` tags its output with and what `up` starts. The
// convention truffels/<id>:latest is only a fallback for a file that names
// nothing usable; staging or restoring a tag nothing runs is the silent no-op
// this path exists to end.
// Every path to the fallback warns: each one is a misconfiguration that turns
// staging and rollback into tag moves nothing runs, and it has to be visible
// before a rollback needs the ref, not afterwards.
func liveImageRef(tmpl model.ServiceTemplate, serviceID string) string {
	ref, _ := composeImageRef(tmpl, serviceID)
	return ref
}

// composeImageRef additionally reports whether the ref was really read off the
// compose file. Only then may the tag be moved: under the fallback we do not
// know what the file says, and rewriting it would be a guess written to disk.
func composeImageRef(tmpl model.ServiceTemplate, serviceID string) (ref string, fromFile bool) {
	fallback := "truffels/" + serviceID + ":latest"
	if tmpl.ComposeDir == "" {
		slog.Warn("no compose dir for service, assuming conventional image ref",
			"service", serviceID, "image", fallback)
		return fallback, false
	}
	path := tmpl.ComposeDir + "/docker-compose.yml"
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("cannot read compose file, assuming conventional image ref",
			"service", serviceID, "path", path, "image", fallback, "err", err)
		return fallback, false
	}
	if m := composeImageRe(serviceID).FindSubmatch(data); m != nil {
		return string(m[1]), true
	}
	// A trailing comment ("image: truffels/ckpool:v1.0.0 # pinned") or an
	// interpolated tag ("image: truffels/ckpool:${TAG}") both miss the pattern.
	slog.Warn("no plain truffels image line in compose file, assuming conventional image ref",
		"service", serviceID, "path", path, "image", fallback)
	return fallback, false
}

// rollbackImageRef names the single retained rollback generation. Exactly one:
// a second update overwrites it, because more generations cost disk on a device
// that has none to spare.
func rollbackImageRef(serviceID string) string {
	return "truffels/" + serviceID + ":rollback"
}

func (e *Engine) applyNeedsBuild(serviceID string, tmpl model.ServiceTemplate, src *model.UpdateSource, check *model.UpdateCheck, logID int64) error {
	// Services whose source lives as a working copy on disk need it moved to the
	// target ref first; ckpool clones inside its Dockerfile and has no RepoDir.
	if src.RepoDir != "" {
		// model.UpdateSource documents an empty RefScheme as "commit", but the
		// agent's /v1/git/checkout treats an empty ref_scheme as "tag" and would
		// reject a commit hash with HTTP 400. Resolve the default here so the
		// wire value always carries the meaning the model defines.
		refScheme := src.RefScheme
		if refScheme == "" {
			refScheme = model.RefSchemeCommit
		}
		if err := e.compose.GitCheckout(src.RepoDir, check.LatestVersion, refScheme); err != nil {
			return e.failUpdate(logID, serviceID, "git checkout failed: "+err.Error(), "")
		}
	}

	// Stage the running image for rollback FIRST — before the tag rewrite below
	// moves the name it is staged from. liveImageRef reads the compose file, so
	// staging after the rewrite would ask docker to tag an image that does not
	// exist yet and leave :rollback pointing into nothing, i.e. a rollback that
	// silently restores the wrong generation or fails outright.
	//
	// Staging itself is best-effort: a first-time build has nothing to stage,
	// and refusing to update over that would be worse than losing the rollback
	// option.
	oldRef, refFromFile := composeImageRef(tmpl, serviceID)
	_, oldTag := splitImageRef(oldRef)
	rollbackRef := rollbackImageRef(serviceID)
	if err := e.compose.ImageTag(oldRef, rollbackRef); err != nil {
		// Whatever :rollback still points at is now one generation too old —
		// it was staged by the *previous* update. Restoring it later would
		// install the wrong build and report a successful rollback, which is
		// the class of lie this whole path exists to end. Drop the tag so a
		// later rollback fails honestly instead. Removing a tag does not delete
		// the image it shares with other tags.
		slog.Warn("could not stage rollback image", "service", serviceID, "image", oldRef, "err", err)
		if rmErr := e.compose.RemoveImage(rollbackRef); rmErr != nil {
			slog.Warn("could not drop the stale rollback tag", "service", serviceID, "image", rollbackRef, "err", rmErr)
		}
	}

	// Move the compose tag onto the version about to be built. It has to happen
	// here — after staging, before the build — because `docker compose build`
	// tags its output with whatever the file says: rewriting afterwards would
	// name a tag no image carries, and the next `up` would rebuild it without a
	// SOURCE_REF arg. This is the same order applySelfUpdate has always used.
	//
	// Best-effort on purpose. The tag is a claim about the image, the source-ref
	// label is the proof, and every check in this file keys off the label. A
	// compose file the agent will not rewrite must not block an update that
	// otherwise works — it only leaves the tag as stale as it already was.
	if refFromFile && len(src.Images) > 0 && oldTag != check.LatestVersion {
		if err := e.compose.RewriteTags(serviceID, src.Images, oldTag, check.LatestVersion); err != nil {
			slog.Warn("could not move the compose image tag onto the new version; the build keeps the old tag",
				"service", serviceID, "old", oldTag, "new", check.LatestVersion, "err", err)
		}
	}

	// Re-read rather than assume the rewrite took: a failure can mean "file
	// untouched" or "written, but the answer was lost on the way back", and what
	// the build produces is whatever the file says now. Everything below —
	// verification and putting the tag back on failure — keys off this ref.
	buildRef := liveImageRef(tmpl, serviceID)

	// restoreComposeTag puts the old tag back when the file really did move.
	// Without it a failed build leaves the compose naming a version whose image
	// is that failed build: the running container is left alone, but any later
	// restart from any cause would pull the broken name up. Returns a suffix for
	// the failure message when even that does not work, because then the file
	// needs a human.
	restoreComposeTag := func() string {
		if buildRef == oldRef {
			return ""
		}
		_, newTag := splitImageRef(buildRef)
		if err := e.compose.RewriteTags(serviceID, src.Images, newTag, oldTag); err != nil {
			slog.Error("could not put the compose image tag back after a failed build",
				"service", serviceID, "image", buildRef, "want", oldTag, "err", err)
			return fmt.Sprintf("; the compose file still names %s — restore the tag to %q before restarting the service", buildRef, oldTag)
		}
		return ""
	}
	fail := func(msg string) error {
		return e.failUpdate(logID, serviceID, msg+restoreComposeTag(), "")
	}

	_ = e.store.UpdateLogStatus(logID, model.UpdateBuilding, "", "")
	buildArgs := map[string]string{"SOURCE_REF": check.LatestVersion}
	if err := e.compose.BuildWithArgs(serviceID, buildArgs); err != nil {
		return fail("build failed: " + err.Error())
	}

	// Verify before restarting: a mismatched image must never get to run.
	//
	// Ask for the ref the compose file names first. The container route resolves
	// {{.Config.Image}} of the container that is *still running the old tag*, so
	// after a rewrite it would inspect the image we just replaced and fail a
	// build that is in fact correct. It stays as a second attempt for the case
	// the by-name lookup produces no ref at all — the agent reports an image it
	// cannot resolve as a 200 with empty fields, which is also what the :latest
	// fallback above looks like when the compose file names something else. That
	// cannot become a hole: after a rewrite the container's image is the old one
	// and its label fails the comparison just the same.
	var built string
	var inspectErrs []string
	if info, err := e.compose.ImageInspectByName(buildRef); err != nil {
		inspectErrs = append(inspectErrs, "by name "+buildRef+": "+err.Error())
	} else {
		built = info.Labels[SourceRefLabel]
	}
	if built == "" && len(tmpl.ContainerNames) > 0 {
		if info, err := e.compose.ImageInspect(tmpl.ContainerNames[0]); err != nil {
			inspectErrs = append(inspectErrs, "via container "+tmpl.ContainerNames[0]+": "+err.Error())
		} else {
			built = info.Labels[SourceRefLabel]
		}
	}
	if built == "" && len(inspectErrs) > 0 {
		return fail("image inspect failed: " + strings.Join(inspectErrs, "; "))
	}
	if built != check.LatestVersion {
		return fail(fmt.Sprintf("built ref %q does not match requested %q", built, check.LatestVersion))
	}

	return nil
}

// selfUpdateImage returns the image repository the compose service svc is built
// into, taken from the template's declared image list rather than assembled
// here. The build loop and the image list are two hand-maintained sequences for
// the same three components; deriving the ref from a string built on the spot
// would let them drift apart silently and verify an image nobody built. Matching
// on the last path element ties them together and reports a miss instead.
func selfUpdateImage(images []string, svc string) (string, bool) {
	for _, img := range images {
		name := img
		if idx := strings.LastIndex(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		if name == svc {
			return img, true
		}
	}
	return "", false
}

// restoreSelfComposeTags puts the pre-update image tags back after the stack
// refused to go through with an update, and returns a suffix for the failure
// message when it could not.
//
// Step 2 moves the compose file onto the new version before anything is built,
// so every refusal after it leaves the file naming images that were just
// rejected. Nothing restarts on its own, but the next `docker compose up` from
// any source — the services API, the compose reconciler, a human — takes the
// file at its word. All three services carry a build: block, so compose would
// then build the missing image itself, without the VERSION arg, and stamp the
// Dockerfile default "dev" into the label: the update refused, and the stack
// silently ends up on an unidentifiable build anyway. Putting the tag back is
// what keeps a refusal a refusal. The agent's rewrite also resets the
// build.args.VERSION line, which step 2 moved for the same reason.
//
// This is deliberately not applyNeedsBuild's restoreComposeTag. That one
// re-reads the single ref a custom-built service runs out of the compose file,
// because there the pre-build rewrite is best-effort and only warns, so what
// the file says afterwards is genuinely unknown. Here the rewrite is fatal on
// failure, the version to go back to is the one the check row already holds,
// and three images move as a set. The shared residue is a single RewriteTags
// call; parameterising one helper for both would cost more than it saves and
// would mean reopening a path that has shipped since dev.24.
//
// Writing the old tags unconditionally is not quite the same as knowing the
// file moved: a RewriteTags error can also mean "written, but the answer was
// lost on the way back", the distinction applyNeedsBuild draws by re-reading.
// That residue is accepted here rather than hidden. Step 2 treats such an error
// as fatal and returns before anything is built, so the only way to reach this
// function is through a rewrite that reported success; and the restore is
// idempotent — the agent replaces whatever tag is on the line, so writing the
// old version over a file that never left it is a no-op that reports "already
// at target version".
func (e *Engine) restoreSelfComposeTags(serviceID string, tmpl model.ServiceTemplate, check *model.UpdateCheck) string {
	if check.CurrentVersion == "" {
		// checkService leaves this empty when it cannot identify what runs. We
		// have no tag to write back and must not invent one — say so instead of
		// leaving the file quietly naming the rejected version.
		slog.Error("cannot restore the compose image tags: the previous version is unknown",
			"service", serviceID, "rejected", check.LatestVersion)
		return fmt.Sprintf("; the compose file still names %s and the previous version is unknown — set the image tags by hand before restarting the stack", check.LatestVersion)
	}
	if check.CurrentVersion == check.LatestVersion {
		return ""
	}
	if err := e.compose.RewriteTags(serviceID, tmpl.UpdateSource.Images, check.LatestVersion, check.CurrentVersion); err != nil {
		slog.Error("could not put the compose image tags back after a refused self-update",
			"service", serviceID, "rejected", check.LatestVersion, "want", check.CurrentVersion, "err", err)
		return fmt.Sprintf("; the compose file still names %s — restore the image tags to %q before restarting the stack", check.LatestVersion, check.CurrentVersion)
	}
	slog.Info("self-update refused; compose image tags restored",
		"service", serviceID, "version", check.CurrentVersion)
	return ""
}

// verifySelfBuild proves that the image just built for one component of the
// truffels stack really carries the version that was requested, and reports an
// error describing the mismatch if it does not. The caller turns that into the
// failure — it owns the update log, the alert and putting the compose tag back.
//
// The build.args.VERSION block in the compose template is what carries the
// version into the binary's ldflag and into the image's OCI version label — but
// "we asked for it" is not "it happened". A template regression, a build arg
// that never reaches the stage stamping the label, or a layer-cache hit on an
// older build all yield an image labelled with the wrong version while the
// build command itself reports success. That is the failure dev.24 closed for
// ckpool and ckstats in applyNeedsBuild; this is the same check for the one
// service that replaces the API, the agent and the web UI at once.
//
// It runs between build and the detached restart on purpose. Once
// ComposeUpDetached fires, the wrong image is live, this API process is gone
// mid-flight and the outcome is left to reconcileStuckUpdates — which can only
// see whether containers came up healthy, not which version they hold. Nothing
// is staged under a :rollback tag for the truffels stack either, so afterwards
// there is nothing to undo it with. Refusing here leaves the old containers
// running and untouched.
func (e *Engine) verifySelfBuild(tmpl model.ServiceTemplate, svc, wantVersion string) error {
	img, ok := selfUpdateImage(tmpl.UpdateSource.Images, svc)
	if !ok {
		return fmt.Errorf("cannot verify the %s build: the update source declares no image for it", svc)
	}
	// Step 2 rewrote the compose tags to the new version before the build, so
	// the image that was just built already answers to the new tag.
	ref := img + ":" + wantVersion

	info, err := e.compose.ImageInspectByName(ref)
	if err != nil {
		return fmt.Errorf("cannot verify the %s build: %w", svc, err)
	}
	// Trimmed for the comparison, so a padded label cannot fail a build that is
	// in fact correct — and so whitespace alone still counts as absent below.
	built := strings.TrimSpace(info.Labels[VersionLabel])
	switch {
	case built == "":
		// Two shapes end up here and both are disqualifying: an image built
		// without the VERSION arg reaching the labelling stage, and no such
		// image at all — the agent answers 200 with empty fields for a name it
		// cannot resolve. An absent label is not a matching label; treating
		// empty as "close enough" is precisely the hole this check exists to
		// close.
		return fmt.Errorf("%s carries no %s label, so the %s build cannot be shown to be %s", ref, VersionLabel, svc, wantVersion)
	case built != wantVersion:
		// Includes the "dev" the Dockerfiles default VERSION to, which is what
		// a dropped build arg looks like from the outside.
		return fmt.Errorf("%s was built as %q, not %q — refusing to restart into it", ref, built, wantVersion)
	}
	return nil
}

// applySelfUpdate handles the self-update flow for truffels services (agent/api/web).
// Flow: git checkout tag → build with VERSION arg → rewrite compose tags → detached restart.
func (e *Engine) applySelfUpdate(serviceID string, tmpl model.ServiceTemplate, check *model.UpdateCheck, logID int64) error {
	// Snapshot compose file
	composePath := tmpl.ComposeDir + "/docker-compose.yml"
	if snapshot, err := os.ReadFile(composePath); err == nil {
		_ = e.store.CreateConfigRevision(&model.ConfigRevision{
			ServiceID:        serviceID,
			Actor:            "update_engine",
			Diff:             fmt.Sprintf("pre-self-update snapshot (%s → %s)", check.CurrentVersion, check.LatestVersion),
			ConfigSnapshot:   string(snapshot),
			ValidationResult: "ok",
		})
	}

	// Step 1: Git checkout the new tag
	_ = e.store.UpdateLogStatus(logID, model.UpdatePulling, "", "")
	if err := e.compose.GitCheckout("/repo", check.LatestVersion, model.RefSchemeTag); err != nil {
		return e.failUpdate(logID, serviceID, "git checkout failed: "+err.Error(), "")
	}

	// Step 2: Rewrite compose image tags BEFORE building so the build
	// creates images tagged with the new version (not the old one).
	if err := e.compose.RewriteTags(serviceID, tmpl.UpdateSource.Images, "", check.LatestVersion); err != nil {
		return e.failUpdate(logID, serviceID, "compose rewrite failed: "+err.Error(), "")
	}

	// Step 3: Build with VERSION arg — build each service sequentially to avoid
	// I/O contention on slower storage (SD cards) causing TLS timeouts during go mod download.
	//
	// Every way out of this step puts the compose file back on the old version
	// first: step 2 already moved it, and a file naming a version that was just
	// rejected is a loaded gun for the next `up` from any source.
	fail := func(msg string) error {
		return e.failUpdate(logID, serviceID, msg+e.restoreSelfComposeTags(serviceID, tmpl, check), "")
	}

	_ = e.store.UpdateLogStatus(logID, model.UpdateBuilding, "", "")
	buildArgs := map[string]string{"VERSION": check.LatestVersion}
	for _, svc := range []string{"agent", "api", "web"} {
		if err := e.compose.BuildWithArgs("truffels-agent", buildArgs, svc); err != nil {
			return fail("build failed (" + svc + "): " + err.Error())
		}
		if err := e.verifySelfBuild(tmpl, svc, check.LatestVersion); err != nil {
			return fail(err.Error())
		}
	}

	// Step 4: Detached restart — agent calls docker compose up -d via nsenter
	// This replaces all containers including the agent itself
	_ = e.store.UpdateLogStatus(logID, model.UpdateRestarting, "", check.CurrentVersion)

	if err := e.compose.ComposeUpDetached("truffels-agent"); err != nil {
		return e.failUpdate(logID, serviceID, "detached restart failed: "+err.Error(), "")
	}

	// The API will be replaced shortly. The "restarting" status will be reconciled
	// when the new API starts up (reconcileStuckUpdates).
	slog.Info("self-update: detached restart initiated, API will restart shortly",
		"service", serviceID, "version", check.LatestVersion)

	return nil
}

// reconcileStuckUpdates checks for update logs stuck in "restarting" status on startup.
// This handles the gap after a self-update where the old API went down and the new one came up.
func (e *Engine) reconcileStuckUpdates() {
	logs, err := e.store.GetUpdateLogsByStatus(model.UpdateRestarting)
	if err != nil {
		slog.Warn("reconcile: failed to query stuck updates", "err", err)
		return
	}
	if len(logs) > 0 {
		slog.Info("reconcile: waiting for services to stabilize before health check", "count", len(logs))
		time.Sleep(e.healthWait)
	}
	for _, l := range logs {
		tmpl, ok := e.registry.Get(l.ServiceID)
		if !ok {
			_ = e.store.UpdateLogStatus(l.ID, model.UpdateFailed, "service not found during reconciliation", "")
			continue
		}

		healthy := e.checkHealth(tmpl)
		if healthy {
			slog.Info("reconcile: update completed successfully", "service", l.ServiceID, "version", l.ToVersion)
			_ = e.store.UpdateLogStatus(l.ID, model.UpdateDone, "", "")
			_ = e.store.ResolveAlerts("update_failed", l.ServiceID)
			_ = e.store.UpsertUpdateCheck(&model.UpdateCheck{
				ServiceID:      l.ServiceID,
				CurrentVersion: l.ToVersion,
				LatestVersion:  l.ToVersion,
				HasUpdate:      false,
			})
			// Prune old images after successful self-update
			if reconTmpl, reconOK := e.registry.Get(l.ServiceID); reconOK && reconTmpl.UpdateSource != nil {
				go e.pruneOldImages(l.ServiceID, reconTmpl.UpdateSource)
			}
		} else {
			slog.Error("reconcile: update appears to have failed", "service", l.ServiceID)
			_ = e.store.UpdateLogStatus(l.ID, model.UpdateFailed, "unhealthy after self-update restart", "")
			e.alertUpdateFailed(l.ServiceID, "unhealthy after self-update restart")
		}
	}
}

// pruneOldImages removes old Docker images after a successful update.
// Keeps the current version (just updated to) and the N-1 version (for rollback).
// Respects the update_keep_old_images setting.
func (e *Engine) pruneOldImages(serviceID string, src *model.UpdateSource) {
	// Check setting
	val, err := e.store.GetSetting("update_keep_old_images")
	if err == nil && val == "true" {
		slog.Info("prune: skipping (update_keep_old_images=true)", "service", serviceID)
		return
	}

	logs, err := e.store.GetUpdateLogs(serviceID, 20)
	if err != nil {
		slog.Warn("prune: failed to get update logs", "service", serviceID, "err", err)
		return
	}

	// Collect versions to keep: current (latest done.ToVersion) and N-1 (fromVersion)
	keepVersions := map[string]bool{}
	for _, l := range logs {
		if l.Status != model.UpdateDone {
			continue
		}
		keepVersions[l.ToVersion] = true
		keepVersions[l.FromVersion] = true
		break
	}

	if len(keepVersions) == 0 {
		return
	}

	// Collect old versions to remove
	var removeVersions []string
	for _, l := range logs {
		if l.Status != model.UpdateDone {
			continue
		}
		// Check both from and to versions
		for _, v := range []string{l.FromVersion, l.ToVersion} {
			if v == "" || keepVersions[v] {
				continue
			}
			removeVersions = append(removeVersions, v)
		}
	}

	// Deduplicate
	seen := map[string]bool{}
	for _, v := range removeVersions {
		if seen[v] {
			continue
		}
		seen[v] = true
		for _, img := range src.Images {
			ref := img + ":" + v
			slog.Info("prune: removing old image", "image", ref)
			if err := e.compose.RemoveImage(ref); err != nil {
				slog.Warn("prune: remove image failed (best-effort)", "image", ref, "err", err)
			}
		}
	}
}

type UpdateError struct {
	Msg string
}

func (e *UpdateError) Error() string {
	return e.Msg
}
