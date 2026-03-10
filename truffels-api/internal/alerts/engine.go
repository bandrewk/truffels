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
}

func NewEngine(s *store.Store, r *service.Registry, c *metrics.Collector) *Engine {
	return &Engine{
		store:             s,
		registry:          r,
		collector:         c,
		stopCh:            make(chan struct{}),
		lastRestartCounts: make(map[string]int),
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

	// Service health alerts
	for _, tmpl := range e.registry.All() {
		e.checkService(tmpl)
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
