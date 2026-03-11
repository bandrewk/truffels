package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
)

func (s *Server) handleMonitoring(w http.ResponseWriter, r *http.Request) {
	// Parse hours param (default 24, clamp 1-48)
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if v, err := strconv.Atoi(h); err == nil {
			hours = v
		}
	}
	if hours < 1 {
		hours = 1
	}
	if hours > 48 {
		hours = 48
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)

	// Container states from all services
	var containers []model.MonitoringContainer
	for _, tmpl := range s.registry.All() {
		states := docker.InspectContainers(tmpl.ContainerNames)
		for _, cs := range states {
			containers = append(containers, model.MonitoringContainer{
				Name:         cs.Name,
				ServiceID:    tmpl.ID,
				DisplayName:  tmpl.DisplayName,
				Status:       cs.Status,
				Health:       cs.Health,
				RestartCount: cs.RestartCount,
				StartedAt:    cs.StartedAt,
				Image:        cs.Image,
			})
		}
	}
	if containers == nil {
		containers = []model.MonitoringContainer{}
	}

	// Metric snapshots (downsampled to ~60 points)
	snapshots, err := s.store.GetMetricSnapshots(since, 60)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if snapshots == nil {
		snapshots = []model.MetricSnapshot{}
	}

	// Compute summary
	summary := computeSummary(snapshots)

	// Current host metrics
	host := s.collector.Collect()

	// Service events
	events, err := s.store.GetServiceEvents(since, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []model.ServiceEvent{}
	}

	// Active alerts
	alerts, err := s.store.GetActiveAlerts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if alerts == nil {
		alerts = []model.Alert{}
	}

	writeJSON(w, http.StatusOK, model.MonitoringResponse{
		Containers: containers,
		Events:     events,
		Metrics: model.MonitoringMetrics{
			Current: &host,
			History: snapshots,
			Summary: summary,
		},
		Alerts: alerts,
	})
}

func (s *Server) handleServiceMonitoring(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tmpl, ok := s.registry.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if v, err := strconv.Atoi(h); err == nil {
			hours = v
		}
	}
	if hours < 1 {
		hours = 1
	}
	if hours > 48 {
		hours = 48
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)

	snapshots, err := s.store.GetContainerSnapshotsByNames(since, tmpl.ContainerNames, 120)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if snapshots == nil {
		snapshots = []model.ContainerSnapshot{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"service_id": id,
		"containers": tmpl.ContainerNames,
		"snapshots":  snapshots,
	})
}

func computeSummary(snapshots []model.MetricSnapshot) model.MetricsSummary {
	if len(snapshots) == 0 {
		return model.MetricsSummary{}
	}

	var s model.MetricsSummary
	n := float64(len(snapshots))

	for _, snap := range snapshots {
		s.CPUAvg += snap.CPUPercent
		s.MemAvg += snap.MemPercent
		s.TempAvg += snap.TempC

		if snap.CPUPercent > s.CPUMax {
			s.CPUMax = snap.CPUPercent
		}
		if snap.MemPercent > s.MemMax {
			s.MemMax = snap.MemPercent
		}
		if snap.TempC > s.TempMax {
			s.TempMax = snap.TempC
		}
	}

	s.CPUAvg /= n
	s.MemAvg /= n
	s.TempAvg /= n

	return s
}
