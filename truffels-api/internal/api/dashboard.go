package api

import (
	"net/http"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": "0.1.0",
	})
}

type dashboardResponse struct {
	Host     model.HostMetrics     `json:"host"`
	Services []dashboardService    `json:"services"`
	Alerts   dashboardAlerts       `json:"alerts"`
}

type dashboardService struct {
	ID          string               `json:"id"`
	DisplayName string               `json:"display_name"`
	State       model.ServiceState   `json:"state"`
	Containers  []model.ContainerState `json:"containers"`
}

type dashboardAlerts struct {
	ActiveCount int           `json:"active_count"`
	Recent      []model.Alert `json:"recent"`
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// Host metrics
	host := s.collector.Collect()

	// Service states
	var services []dashboardService
	for _, tmpl := range s.registry.All() {
		containers := docker.InspectContainers(tmpl.ContainerNames)
		state := deriveState(containers)
		services = append(services, dashboardService{
			ID:          tmpl.ID,
			DisplayName: tmpl.DisplayName,
			State:       state,
			Containers:  containers,
		})
	}

	// Alerts
	activeAlerts, _ := s.store.GetActiveAlerts()
	if activeAlerts == nil {
		activeAlerts = []model.Alert{}
	}
	recent := activeAlerts
	if len(recent) > 5 {
		recent = recent[:5]
	}

	writeJSON(w, http.StatusOK, dashboardResponse{
		Host:     host,
		Services: services,
		Alerts: dashboardAlerts{
			ActiveCount: len(activeAlerts),
			Recent:      recent,
		},
	})
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.collector.Collect())
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	var alerts []model.Alert
	var err error

	if r.URL.Query().Get("all") == "true" {
		alerts, err = s.store.GetAllAlerts(100)
	} else {
		alerts, err = s.store.GetActiveAlerts()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if alerts == nil {
		alerts = []model.Alert{}
	}
	writeJSON(w, http.StatusOK, alerts)
}

// deriveState computes the overall service state from container states.
func deriveState(containers []model.ContainerState) model.ServiceState {
	if len(containers) == 0 {
		return model.StateUnknown
	}

	allRunning := true
	anyRunning := false

	for _, c := range containers {
		if c.Status == "running" {
			anyRunning = true
		} else {
			allRunning = false
		}
	}

	if allRunning {
		// Check if any are unhealthy
		for _, c := range containers {
			if c.Health == "unhealthy" {
				return model.StateDegraded
			}
		}
		return model.StateRunning
	}
	if anyRunning {
		return model.StateDegraded
	}
	return model.StateStopped
}
