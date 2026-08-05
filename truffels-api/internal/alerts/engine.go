package alerts

import (
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"truffels-api/internal/docker"
	"truffels-api/internal/metrics"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

type Engine struct {
	store     *store.Store
	registry  *service.Registry
	collector *metrics.Collector
	compose   *docker.ComposeClient
	stopCh    chan struct{}

	// Track restart counts for restart-loop detection
	lastRestartCounts map[string]int

	// Track restart timestamps for windowed restart-loop detection
	restartHistory map[string][]time.Time

	// Track services that were auto-stopped to avoid repeated stops
	autoStopped map[string]bool

	// Monitoring: cached previous container states for change detection
	prevStates map[string]model.ContainerState

	// Monitoring: counter to record every other tick (60s intervals)
	snapshotTick int

	// Monitoring: previous container stats for delta computation
	prevContainerStats map[string]docker.ContainerResourceStats

	// containerStartedAt records the first StartedAt the engine observes per
	// container after its own boot. Used by the trend gate to detect
	// containers that restarted around the same time as the engine itself
	// (e.g. during truffels stack self-update) — those restarts can't be
	// detected from RestartCount deltas because the engine had no prior
	// baseline.
	containerStartedAt map[string]time.Time

	// reclaiming guards tryReclaim so at most one stop/clear/start cycle
	// runs at a time. tryReclaim runs on its own goroutine because each
	// ComposeClient call (Stop/FsClearDir/Up) can take up to the agent's
	// 6-minute HTTP timeout — up to four such calls back to back would
	// otherwise stall this engine's single evaluate() goroutine for ~24
	// minutes, during which no other alert (temperature, disk, container
	// health, restart-loop) would be checked and no metric snapshot would
	// be written.
	reclaiming atomic.Bool

	// reclaimDone, when non-nil, receives one value after each tryReclaim
	// goroutine finishes. Test-only synchronization hook so tests can wait
	// for the goroutine instead of sleeping; production code never sets it.
	reclaimDone chan struct{}
}

func NewEngine(s *store.Store, r *service.Registry, c *metrics.Collector, compose *docker.ComposeClient) *Engine {
	return &Engine{
		store:              s,
		registry:           r,
		collector:          c,
		compose:            compose,
		stopCh:             make(chan struct{}),
		lastRestartCounts:  make(map[string]int),
		restartHistory:     make(map[string][]time.Time),
		autoStopped:        make(map[string]bool),
		prevStates:         make(map[string]model.ContainerState),
		prevContainerStats: make(map[string]docker.ContainerResourceStats),
		containerStartedAt: make(map[string]time.Time),
		// Initialize snapshotTick to 9 so the first ++ on first evaluate
		// makes it 10 — triggering both the metric-snapshot (%2==0) AND
		// the trend check (%10==0) immediately. Otherwise stale memory_trend
		// alerts from the prior engine instance linger for up to 5 min
		// (one full trend tick window) after every truffels self-update.
		snapshotTick: 9,
	}
}

func (e *Engine) Start() {
	go e.loop()
}

func (e *Engine) Stop() {
	close(e.stopCh)
}

func (e *Engine) loop() {
	// Initial evaluation after short delay
	time.Sleep(5 * time.Second)
	e.evaluate()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			e.evaluate()
		case <-e.stopCh:
			return
		}
	}
}

func (e *Engine) evaluate() {
	if e.collector == nil {
		return
	}
	host := e.collector.Collect()

	// Disk usage alerts
	for _, disk := range host.Disks {
		e.checkDisk(disk)
	}

	// Temperature alerts (configurable thresholds)
	e.checkTemp(host.Temperature)

	// ---- Monitoring: record metric snapshot every other tick (60s) ----
	e.snapshotTick++
	if e.snapshotTick%2 == 0 {
		diskPercent := 0.0
		if len(host.Disks) > 0 {
			diskPercent = host.Disks[0].UsedPercent
		}
		if err := e.store.InsertMetricSnapshot(host.CPUPercent, host.MemPercent, host.Temperature, diskPercent, host.FanRPM, host.FanPercent,
			host.NetRxBytes, host.NetTxBytes, host.DiskReadBytes, host.DiskWriteBytes, host.DiskIOPercent); err != nil {
			slog.Error("insert metric snapshot", "err", err)
		}

		// Per-container resource snapshots (with delta computation for I/O)
		if stats, err := docker.Stats(); err != nil {
			slog.Error("collect container stats", "err", err)
		} else if len(stats) > 0 {
			snaps := make([]model.ContainerSnapshot, 0, len(stats))
			for _, s := range stats {
				snap := model.ContainerSnapshot{
					Container:  s.Name,
					CPUPercent: s.CPUPercent,
					MemUsageMB: s.MemUsageMB,
					MemLimitMB: s.MemLimitMB,
				}
				// Compute deltas from previous sample
				if prev, ok := e.prevContainerStats[s.Name]; ok {
					snap.NetRxBytes = clampDelta(s.NetRxBytes, prev.NetRxBytes)
					snap.NetTxBytes = clampDelta(s.NetTxBytes, prev.NetTxBytes)
					snap.BlockReadBytes = clampDelta(s.BlockReadBytes, prev.BlockReadBytes)
					snap.BlockWriteBytes = clampDelta(s.BlockWriteBytes, prev.BlockWriteBytes)
				}
				// else: first sample, deltas stay 0
				e.prevContainerStats[s.Name] = s
				snaps = append(snaps, snap)
			}
			if err := e.store.InsertContainerSnapshots(snaps); err != nil {
				slog.Error("insert container snapshots", "err", err)
			}
		}

		// Watched data-dir sizes: poll once per 60s tick, persist, evaluate.
		// Currently mempool cache only — the path that has historically grown
		// unbounded (rbfcache.json) and OOM'd the backend. Easy to extend.
		e.evalWatchedDirs()
	}

	// Service health alerts + monitoring state change detection
	for _, tmpl := range e.registry.All() {
		e.checkService(tmpl)
	}

	// Dependency health checks
	e.checkDependencyHealth()

	// ---- Trend alerts: every 10th tick (~5 minutes) ----
	if e.snapshotTick%10 == 0 {
		e.checkTrends()
	}

	// ---- Monitoring: prune old data every 100th tick (~50 minutes) ----
	if e.snapshotTick%100 == 0 {
		if err := e.store.PruneMetricSnapshots(time.Now().Add(-168 * time.Hour)); err != nil {
			slog.Error("prune metric snapshots", "err", err)
		}
		if err := e.store.PruneServiceEvents(500); err != nil {
			slog.Error("prune service events", "err", err)
		}
		if err := e.store.PruneContainerSnapshots(time.Now().Add(-168 * time.Hour)); err != nil {
			slog.Error("prune container snapshots", "err", err)
		}
		if err := e.store.PruneDirSizeSnapshots(time.Now().Add(-168 * time.Hour)); err != nil {
			slog.Error("prune dir size snapshots", "err", err)
		}
	}
}

// watchedDir is a data-dir we track size for. Currently the only entry is
// mempool's cache — added after the dev.20 rbfcache.json runaway that
// OOM'd the backend.
type watchedDir struct {
	serviceID  string
	path       string
	humanLabel string
	// Setting keys for the thresholds, so an operator can tune them without
	// a release. Defaults live in the getSettingInt calls in evalWatchedDirs.
	warnKey     string
	criticalKey string
}

var watchedDirs = []watchedDir{
	{
		serviceID:   "mempool",
		path:        "/srv/truffels/data/mempool/cache",
		humanLabel:  "mempool cache",
		warnKey:     "dir_size_warning_mb",
		criticalKey: "dir_size_critical_mb",
	},
}

// evalWatchedDirs polls each watched data-dir size via the agent, persists a
// snapshot, and upserts/resolves alerts at the configured thresholds.
func (e *Engine) evalWatchedDirs() {
	if e.compose == nil {
		return
	}
	for _, w := range watchedDirs {
		size, _, err := e.compose.HostDirSize(w.path)
		if err != nil {
			slog.Debug("dir-size fetch failed", "path", w.path, "err", err)
			continue
		}
		if size < 0 {
			// Agent hasn't walked this path yet — no data to persist or alert.
			continue
		}
		if err := e.store.InsertDirSizeSnapshot(w.path, size); err != nil {
			slog.Error("insert dir size snapshot", "path", w.path, "err", err)
		}

		warnBytes := int64(e.getSettingInt(w.warnKey, 700)) * 1024 * 1024
		criticalBytes := int64(e.getSettingInt(w.criticalKey, 900)) * 1024 * 1024

		critType := "dir_size_critical"
		warnType := "dir_size_warning"
		mb := size / (1024 * 1024)
		switch {
		case size >= criticalBytes:
			tail := "Stop the service and clear the dir via Settings → Data Dirs."
			if e.getSettingStr("dir_size_autoreclaim_enabled", "true") == "true" {
				tail = "It will be cleared automatically and the service restarted (~60 s)."
			}
			e.upsert(critType, w.serviceID, model.SeverityCritical,
				"%s reached %d MB — will OOM the backend on next restart. %s",
				w.humanLabel, mb, tail)
			e.resolve(warnType, w.serviceID)
		case size >= warnBytes:
			e.upsert(warnType, w.serviceID, model.SeverityWarning,
				"%s reached %d MB — approaching the size that caused the dev.20 OOM. Consider clearing via Settings → Data Dirs.",
				w.humanLabel, mb)
			e.resolve(critType, w.serviceID)
		default:
			e.resolve(warnType, w.serviceID)
			e.resolve(critType, w.serviceID)
		}

		if size >= criticalBytes {
			if e.reclaiming.CompareAndSwap(false, true) {
				go func(w watchedDir, size, criticalBytes int64) {
					defer func() {
						e.reclaiming.Store(false)
						if e.reclaimDone != nil {
							e.reclaimDone <- struct{}{}
						}
					}()
					e.tryReclaim(w, size, criticalBytes)
				}(w, size, criticalBytes)
			} else {
				slog.Debug("auto-reclaim: previous attempt still running, skipping this tick",
					"service", w.serviceID)
			}
		}
	}
}

// tryReclaim executes the stop → clear → start cycle the dir_size_critical
// alert prescribes, once every guardrail in decideReclaim passes. Called on
// its own goroutine (see evalWatchedDirs) — never called concurrently with
// itself because of the e.reclaiming CAS guard, but still runs concurrently
// with the rest of the engine's evaluate() loop, so it must not touch any of
// the Engine's maps.
//
// It resolves the target through the service template's DataDirs rather than
// trusting w.path directly: a directory that is not declared Clearable and
// RequiresStop cannot be reached from here, so entries like mempool/mysql are
// structurally out of range.
func (e *Engine) tryReclaim(w watchedDir, size, criticalBytes int64) {
	tmpl, ok := e.registry.Get(w.serviceID)
	if !ok {
		return
	}

	var target *model.DataDir
	for i := range tmpl.DataDirs {
		if tmpl.DataDirs[i].Path == w.path {
			target = &tmpl.DataDirs[i]
			break
		}
	}
	if target == nil {
		slog.Warn("auto-reclaim: watched dir is not declared in DataDirs",
			"service", w.serviceID, "path", w.path)
		return
	}

	running := false
	for _, name := range tmpl.ContainerNames {
		if cs, err := docker.InspectContainer(name); err == nil && cs.Status == "running" {
			running = true
			break
		}
	}

	last, hasLast, err := e.store.LastAuditAt("auto_reclaim", w.serviceID)
	if err != nil {
		slog.Error("auto-reclaim: audit lookup failed", "service", w.serviceID, "err", err)
		return
	}

	decision := decideReclaim(reclaimInput{
		SizeBytes:          size,
		CriticalBytes:      criticalBytes,
		Enabled:            e.getSettingStr("dir_size_autoreclaim_enabled", "true") == "true",
		MinInterval:        time.Duration(e.getSettingInt("dir_size_autoreclaim_min_interval_hours", 24)) * time.Hour,
		LastReclaim:        last,
		HasLastReclaim:     hasLast,
		Now:                time.Now().UTC(),
		ServiceRunning:     running,
		TargetClearable:    target.Clearable,
		TargetRequiresStop: target.RequiresStop,
	})
	if !decision.Act {
		slog.Debug("auto-reclaim skipped", "service", w.serviceID, "reason", decision.Reason)
		return
	}

	mb := size / (1024 * 1024)

	// Log the attempt BEFORE touching the service, not on success. The 24h
	// cooldown above is enforced by decideReclaim reading this exact row
	// back via LastAuditAt("auto_reclaim", serviceID) on the next tick. If
	// this were only written after a successful Up, a persistent failure
	// (e.g. clear-dir erroring every time) would leave no row for
	// LastAuditAt to find, decideReclaim would never see a prior attempt,
	// and the engine would stop and restart the service on every 30s tick
	// forever.
	//
	// This ordering makes the sequence loop-safe, not availability-safe: if
	// truffels-api crashes between Stop and Up (after this write succeeded),
	// the cooldown blocks any retry for dir_size_autoreclaim_min_interval_hours
	// and recovery is manual — the compose reconciler only issues Up when the
	// compose file itself changed, not on every tick.
	//
	// If the write itself fails (e.g. SQLITE_FULL or an I/O error under disk
	// pressure — precisely the condition this feature exists to relieve), we
	// must not proceed to Stop: without this row on record, the cooldown
	// guard above can't see this attempt on the next tick, and the engine
	// would stop and restart the service forever. Refusing to act is the
	// safe failure here — an oversized cache is a known, alerted condition;
	// an unbounded stop/start loop is not.
	if err := e.store.LogAudit("auto_reclaim", w.serviceID,
		fmt.Sprintf("attempting to clear %s (%d MB) and restart the service", target.Path, mb), ""); err != nil {
		e.reclaimFailed(w, false, fmt.Sprintf(
			"could not record the reclaim attempt: %s — aborting before stopping the service to avoid an uncontrolled restart loop", err.Error()))
		return
	}

	slog.Info("auto-reclaim starting", "service", w.serviceID, "path", target.Path, "size_mb", mb)

	if err := e.compose.Stop(w.serviceID); err != nil {
		// A Stop-request timeout can still have taken effect on the agent
		// side, so we can't assert the service is still running here —
		// treat it as possibly down.
		e.reclaimFailed(w, true, "stop failed: "+err.Error())
		return
	}
	if err := e.compose.FsClearDir(target.Path, 1000, 1000, "0755"); err != nil {
		// Bring the service back before reporting — a stopped service is
		// worse than a full cache.
		if upErr := e.compose.Up(w.serviceID); upErr != nil {
			e.reclaimFailed(w, true, fmt.Sprintf(
				"clear-dir failed: %s; restart after failed clear also failed: %s", err.Error(), upErr.Error()))
			return
		}
		e.reclaimFailed(w, false, "clear-dir failed: "+err.Error())
		return
	}
	if err := e.compose.Up(w.serviceID); err != nil {
		// One retry: this is the dangerous state, the service is down.
		if err2 := e.compose.Up(w.serviceID); err2 != nil {
			e.reclaimFailed(w, true, "service did not restart after clear: "+err2.Error())
			return
		}
	}

	e.resolve("auto_reclaim_failed", w.serviceID)
	slog.Info("auto-reclaim complete", "service", w.serviceID, "reclaimed_mb", mb)
}

// reclaimFailed raises a critical alert and writes the failure to the audit
// log. There is no in-process retry here — the "auto_reclaim" audit row
// tryReclaim writes before it ever touches the service is what stops a
// repeated stop/start loop, by consuming the cooldown decideReclaim checks
// on the next tick regardless of whether this attempt succeeded.
//
// serviceMayBeDown selects the remediation text: several failure paths
// leave the service stopped (or in an unknown state after a Stop timeout),
// and telling the operator to "clear manually" without mentioning that is
// misleading — they need to start the service first.
func (e *Engine) reclaimFailed(w watchedDir, serviceMayBeDown bool, detail string) {
	slog.Error("auto-reclaim failed", "service", w.serviceID, "detail", detail, "service_may_be_down", serviceMayBeDown)
	_ = e.store.LogAudit("auto_reclaim_failed", w.serviceID, detail, "")

	remediation := "clear it manually via Settings → Data Dirs"
	if serviceMayBeDown {
		remediation = "the service may be stopped — check its status in Services and start it, then clear the cache manually via Settings → Data Dirs if it's still oversized"
	}
	e.upsert("auto_reclaim_failed", w.serviceID, model.SeverityCritical,
		"automatic %s reclaim failed: %s — %s",
		w.humanLabel, detail, remediation)
}

func (e *Engine) checkDisk(disk model.DiskUsage) {
	alertType := "disk_full"
	serviceID := ""

	if disk.UsedPercent >= 95 {
		e.upsert(alertType, serviceID, model.SeverityCritical,
			"Disk usage critical: %.1f%% used on %s (%.1fGB free)", disk.UsedPercent, disk.Path, disk.AvailGB)
	} else if disk.UsedPercent >= 90 {
		e.upsert(alertType, serviceID, model.SeverityWarning,
			"Disk usage high: %.1f%% used on %s (%.1fGB free)", disk.UsedPercent, disk.Path, disk.AvailGB)
	} else {
		e.resolve(alertType, serviceID)
	}
}

func (e *Engine) checkTemp(tempC float64) {
	alertType := "high_temp"
	serviceID := ""

	critical := e.getSettingFloat("temp_critical", 80)
	warning := e.getSettingFloat("temp_warning", 75)

	if tempC >= critical {
		e.upsert(alertType, serviceID, model.SeverityCritical,
			"CPU temperature critical: %.1f°C (threshold: %.0f°C)", tempC, critical)
	} else if tempC >= warning {
		e.upsert(alertType, serviceID, model.SeverityWarning,
			"CPU temperature high: %.1f°C (threshold: %.0f°C)", tempC, warning)
	} else {
		e.resolve(alertType, serviceID)
	}
}

func (e *Engine) checkService(tmpl model.ServiceTemplate) {
	enabled, _ := e.store.IsServiceEnabled(tmpl.ID)
	// For read-only services (DBs, proxy), suppress exited alerts if all
	// dependent services are disabled — the user can't control these directly.
	if tmpl.ReadOnly && enabled {
		if deps := e.registry.Dependents(tmpl.ID); len(deps) > 0 {
			allDisabled := true
			for _, depID := range deps {
				depEnabled, _ := e.store.IsServiceEnabled(depID)
				if depEnabled {
					allDisabled = false
					break
				}
			}
			if allDisabled {
				enabled = false
			}
		}
	}
	threshold := e.getSettingInt("restart_loop_count", 5)
	windowMin := e.getSettingInt("restart_loop_window_min", 10)
	maxRetries := e.getSettingInt("restart_loop_max_retries", 10)

	for _, name := range tmpl.ContainerNames {
		cs, err := docker.InspectContainer(name)
		if err != nil {
			continue
		}

		// Unhealthy check
		alertType := "service_unhealthy"
		if cs.Health == "unhealthy" {
			e.upsert(alertType, tmpl.ID, model.SeverityCritical,
				"Container %s is unhealthy", name)
		} else if cs.Status == "exited" || cs.Status == "not_found" {
			if enabled {
				e.upsert(alertType, tmpl.ID, model.SeverityWarning,
					"Container %s is %s", name, cs.Status)
			} else {
				e.resolve(alertType, tmpl.ID)
			}
		} else {
			e.resolve(alertType, tmpl.ID)
		}

		// Record container's StartedAt on first observation per engine
		// lifetime — used by the trend gate to skip containers that
		// restarted concurrently with the engine itself (when no
		// RestartCount delta could be observed).
		if _, seen := e.containerStartedAt[name]; !seen && cs.StartedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, cs.StartedAt); err == nil {
				e.containerStartedAt[name] = t
			}
		}

		// Restart loop detection (windowed)
		e.recordRestartIncrements(name, cs.RestartCount)
		e.evalRestartLoop(tmpl.ID, name, threshold, windowMin, maxRetries)

		// ---- Monitoring: detect state/health changes and restarts ----
		prev, hasPrev := e.prevStates[name]
		if hasPrev {
			if prev.Status != cs.Status {
				msg := fmt.Sprintf("Container %s status changed: %s -> %s", name, prev.Status, cs.Status)
				if err := e.store.InsertServiceEvent(tmpl.ID, name, "state_change", prev.Status, cs.Status, msg); err != nil {
					slog.Error("insert service event", "err", err)
				}
			}
			if prev.Health != cs.Health {
				msg := fmt.Sprintf("Container %s health changed: %s -> %s", name, prev.Health, cs.Health)
				if err := e.store.InsertServiceEvent(tmpl.ID, name, "health_change", prev.Health, cs.Health, msg); err != nil {
					slog.Error("insert service event", "err", err)
				}
			}
			if cs.RestartCount > prev.RestartCount {
				msg := fmt.Sprintf("Container %s restarted (%d -> %d)", name, prev.RestartCount, cs.RestartCount)
				if err := e.store.InsertServiceEvent(tmpl.ID, name, "restart",
					fmt.Sprintf("%d", prev.RestartCount), fmt.Sprintf("%d", cs.RestartCount), msg); err != nil {
					slog.Error("insert service event", "err", err)
				}
			}
		}
		e.prevStates[name] = cs
	}
}

func (e *Engine) checkDependencyHealth() {
	mode := e.getSettingStr("dep_handling_mode", "flag_only")

	for _, tmpl := range e.registry.All() {
		if len(tmpl.Dependencies) == 0 {
			continue
		}
		enabled, _ := e.store.IsServiceEnabled(tmpl.ID)
		if !enabled {
			e.resolve("upstream_unhealthy", tmpl.ID)
			continue
		}

		for _, depID := range tmpl.Dependencies {
			depTmpl, ok := e.registry.Get(depID)
			if !ok {
				continue
			}

			upstreamDown := false
			for _, name := range depTmpl.ContainerNames {
				cs, err := docker.InspectContainer(name)
				if err != nil {
					continue
				}
				if cs.Health == "unhealthy" || cs.Status == "exited" || cs.Status == "restarting" || cs.Status == "not_found" {
					upstreamDown = true
					break
				}
			}

			alertType := "upstream_unhealthy"
			if upstreamDown {
				e.upsert(alertType, tmpl.ID, model.SeverityWarning,
					"Upstream dependency %s is unhealthy — %s may be serving stale data",
					depID, tmpl.DisplayName)

				if mode == "flag_and_stop" && e.compose != nil && !e.autoStopped[tmpl.ID+"_dep"] {
					slog.Warn("auto-stopping dependent service",
						"service", tmpl.ID, "upstream", depID)
					// Stop, not Down: containers should remain in Exited state so
					// the user can recover with one click in the UI. Down removes
					// them entirely and the only way back is `compose up`.
					if err := e.compose.Stop(tmpl.ID); err != nil {
						slog.Error("auto-stop dependent failed", "service", tmpl.ID, "err", err)
					} else {
						e.autoStopped[tmpl.ID+"_dep"] = true
						_ = e.store.LogAudit("auto_stop_dependency", tmpl.ID,
							fmt.Sprintf("Service %s auto-stopped: upstream %s is unhealthy", tmpl.ID, depID), "alert-engine")
					}
				}
			} else {
				e.resolve(alertType, tmpl.ID)
				delete(e.autoStopped, tmpl.ID+"_dep")
			}
		}
	}
}

func (e *Engine) checkTrends() {
	enabled := e.getSettingStr("trend_alert_enabled", "true")
	if enabled != "true" {
		return
	}

	horizon := e.getSettingFloat("trend_alert_horizon_hours", 6)
	lookback := e.getSettingFloat("trend_alert_lookback_hours", 6)
	minDataHours := e.getSettingFloat("trend_alert_min_data_hours", 2)

	// Check if we have enough data
	since := time.Now().Add(-time.Duration(lookback) * time.Hour)
	oldestAllowed := time.Now().Add(-time.Duration(minDataHours) * time.Hour)

	// Containers that restarted within the lookback window — skip their
	// trend evaluation (post-restart memory growth is cache fill, not a leak).
	restartedContainers := make(map[string]bool)
	if events, err := e.store.GetServiceEvents(since, 1000); err == nil {
		for _, ev := range events {
			if ev.EventType == "restart" {
				restartedContainers[ev.Container] = true
			}
		}
	}
	// Also mark containers whose StartedAt (first observed by this engine
	// instance) falls within the lookback window. Catches the case where
	// the engine restarted concurrently with its containers (truffels
	// stack self-update) — no restart event was inserted because there
	// was no prior baseline to compare RestartCount against.
	for name, startedAt := range e.containerStartedAt {
		if startedAt.After(since) {
			restartedContainers[name] = true
		}
	}

	// Container memory trends
	containerAlerts := evaluateContainerMemoryTrends(e.store, lookback, horizon, oldestAllowed, restartedContainers)
	activeContainers := make(map[string]bool)
	for _, pa := range containerAlerts {
		activeContainers[pa.ServiceID] = true
		e.upsert(pa.AlertType, pa.ServiceID, model.SeverityWarning, "%s", pa.Message)
	}

	// Resolve container memory_trend alerts that no longer fire
	existingAlerts, _ := e.store.GetActiveAlerts()
	for _, a := range existingAlerts {
		if a.Type == "memory_trend" && a.ServiceID != "" && !activeContainers[a.ServiceID] {
			e.resolve("memory_trend", a.ServiceID)
		}
	}

	// Host trends — only if we have enough data
	snaps, _ := e.store.GetMetricSnapshotsForTrend(since)
	if len(snaps) > 0 && snaps[0].Timestamp.Before(oldestAllowed) {
		tempCritical := e.getSettingFloat("temp_critical", 80)
		hostAlerts := evaluateHostTrends(e.store, lookback, horizon, tempCritical)
		activeHostTypes := make(map[string]bool)
		for _, pa := range hostAlerts {
			activeHostTypes[pa.AlertType] = true
			e.upsert(pa.AlertType, pa.ServiceID, model.SeverityWarning, "%s", pa.Message)
		}

		// Resolve host trend alerts that no longer fire
		for _, alertType := range []string{"disk_trend", "temp_trend"} {
			if !activeHostTypes[alertType] {
				e.resolve(alertType, "")
			}
		}
		// Host memory_trend (serviceID="")
		if !activeHostTypes["memory_trend"] {
			e.resolve("memory_trend", "")
		}
	}
}

func (e *Engine) upsert(alertType, serviceID string, severity model.AlertSeverity, msgFmt string, args ...interface{}) {
	msg := alertType
	if len(args) > 0 {
		msg = sprintf(msgFmt, args...)
	}
	if err := e.store.UpsertAlert(&model.Alert{
		Type:      alertType,
		Severity:  severity,
		ServiceID: serviceID,
		Message:   msg,
	}); err != nil {
		slog.Error("upsert alert", "err", err)
	}
}

func (e *Engine) resolve(alertType, serviceID string) {
	if err := e.store.ResolveAlerts(alertType, serviceID); err != nil {
		slog.Error("resolve alert", "err", err)
	}
}

func sprintf(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

// clampDelta returns cur - prev, clamped to 0 on counter reset (container restart).
func clampDelta(cur, prev int64) int64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

// recordRestartIncrements records new restart timestamps when the restart count increases.
func (e *Engine) recordRestartIncrements(containerName string, currentCount int) {
	prevCount, exists := e.lastRestartCounts[containerName]
	e.lastRestartCounts[containerName] = currentCount
	if exists && currentCount > prevCount {
		for i := 0; i < currentCount-prevCount; i++ {
			e.restartHistory[containerName] = append(e.restartHistory[containerName], time.Now())
		}
	}
}

// evalRestartLoop evaluates restart history for a container and fires/resolves alerts.
func (e *Engine) evalRestartLoop(serviceID, containerName string, threshold, windowMin, maxRetries int) {
	cutoff := time.Now().Add(-time.Duration(windowMin) * time.Minute)
	var recent []time.Time
	for _, t := range e.restartHistory[containerName] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	e.restartHistory[containerName] = recent

	if len(recent) >= threshold {
		e.upsert("restart_loop", serviceID, model.SeverityCritical,
			"Container %s restarted %d times in %d minutes (threshold: %d)",
			containerName, len(recent), windowMin, threshold)

		if maxRetries > 0 && len(recent) >= maxRetries && !e.autoStopped[serviceID] {
			slog.Warn("auto-stopping service due to restart loop",
				"service", serviceID, "restarts", len(recent), "max", maxRetries)
			if e.compose != nil {
				// Stop, not Down: containers remain in Exited state so the user
				// can recover from the UI Start button. Down vaporises the stack
				// and the next compose up is the only way back (see dev.20 incident
				// where mempool auto-stop wiped the containers entirely).
				if err := e.compose.Stop(serviceID); err != nil {
					slog.Error("auto-stop failed", "service", serviceID, "err", err)
				} else {
					e.autoStopped[serviceID] = true
					_ = e.store.LogAudit("auto_stop", serviceID,
						fmt.Sprintf("Service %s auto-stopped: %d restarts in %d minutes exceeded max %d",
							serviceID, len(recent), windowMin, maxRetries), "alert-engine")
				}
			}
		}
	} else if len(recent) == 0 {
		e.resolve("restart_loop", serviceID)
		delete(e.autoStopped, serviceID)
	}
}

func (e *Engine) getSettingStr(key, def string) string {
	val, err := e.store.GetSetting(key)
	if err != nil || val == "" {
		return def
	}
	return val
}

func (e *Engine) getSettingInt(key string, def int) int {
	val := e.getSettingStr(key, "")
	if val == "" {
		return def
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return def
	}
	return n
}

func (e *Engine) getSettingFloat(key string, def float64) float64 {
	val := e.getSettingStr(key, "")
	if val == "" {
		return def
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return def
	}
	return f
}
