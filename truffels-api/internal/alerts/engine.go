package alerts

import (
	"fmt"
	"log/slog"
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
	stopCh    chan struct{}

	// Track restart counts for restart-loop detection
	lastRestartCounts map[string]int

	// Monitoring: cached previous container states for change detection
	prevStates map[string]model.ContainerState

	// Monitoring: counter to record every other tick (60s intervals)
	snapshotTick int
}

func NewEngine(s *store.Store, r *service.Registry, c *metrics.Collector) *Engine {
	return &Engine{
		store:             s,
		registry:          r,
		collector:         c,
		stopCh:            make(chan struct{}),
		lastRestartCounts: make(map[string]int),
		prevStates:        make(map[string]model.ContainerState),
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
	host := e.collector.Collect()

	// Disk usage alerts
	for _, disk := range host.Disks {
		e.checkDisk(disk)
	}

	// Temperature alerts
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

		// Per-container resource snapshots
		if stats, err := docker.Stats(); err != nil {
			slog.Error("collect container stats", "err", err)
		} else if len(stats) > 0 {
			snaps := make([]model.ContainerSnapshot, len(stats))
			for i, s := range stats {
				snaps[i] = model.ContainerSnapshot{
					Container:       s.Name,
					CPUPercent:      s.CPUPercent,
					MemUsageMB:      s.MemUsageMB,
					MemLimitMB:      s.MemLimitMB,
					NetRxBytes:      s.NetRxBytes,
					NetTxBytes:      s.NetTxBytes,
					BlockReadBytes:  s.BlockReadBytes,
					BlockWriteBytes: s.BlockWriteBytes,
				}
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

	if tempC >= 80 {
		e.upsert(alertType, serviceID, model.SeverityCritical,
			"CPU temperature critical: %.1f°C", tempC)
	} else if tempC >= 75 {
		e.upsert(alertType, serviceID, model.SeverityWarning,
			"CPU temperature high: %.1f°C", tempC)
	} else {
		e.resolve(alertType, serviceID)
	}
}

func (e *Engine) checkService(tmpl model.ServiceTemplate) {
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
			e.upsert(alertType, tmpl.ID, model.SeverityWarning,
				"Container %s is %s", name, cs.Status)
		} else {
			e.resolve(alertType, tmpl.ID)
		}

		// Restart loop detection
		restartKey := name
		prevCount, exists := e.lastRestartCounts[restartKey]
		e.lastRestartCounts[restartKey] = cs.RestartCount
		if exists && cs.RestartCount-prevCount >= 3 {
			e.upsert("restart_loop", tmpl.ID, model.SeverityCritical,
				"Container %s restarted %d times recently", name, cs.RestartCount-prevCount)
		} else if exists && cs.RestartCount == prevCount {
			e.resolve("restart_loop", tmpl.ID)
		}

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
