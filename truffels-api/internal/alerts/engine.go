package alerts

import (
	"fmt"
	"log/slog"
	"strconv"
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
	}

	// Service health alerts + monitoring state change detection
	for _, tmpl := range e.registry.All() {
		e.checkService(tmpl)
	}

	// Dependency health checks
	e.checkDependencyHealth()

	// ---- Monitoring: prune old data every 100th tick (~50 minutes) ----
	if e.snapshotTick%100 == 0 {
		if err := e.store.PruneMetricSnapshots(time.Now().Add(-48 * time.Hour)); err != nil {
			slog.Error("prune metric snapshots", "err", err)
		}
		if err := e.store.PruneServiceEvents(500); err != nil {
			slog.Error("prune service events", "err", err)
		}
		if err := e.store.PruneContainerSnapshots(time.Now().Add(-48 * time.Hour)); err != nil {
			slog.Error("prune container snapshots", "err", err)
		}
	}
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
	maxRetries := e.getSettingInt("restart_loop_max_retries", 0)

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
					if err := e.compose.Down(tmpl.ID); err != nil {
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
				if err := e.compose.Down(serviceID); err != nil {
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
