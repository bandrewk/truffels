package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
	"truffels-api/internal/model"
)

var agentClient *AgentInspector

type AgentInspector struct {
	agentURL   string
	httpClient *http.Client
}

func NewAgentInspector(agentURL string) *AgentInspector {
	ai := &AgentInspector{
		agentURL: agentURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	agentClient = ai
	return ai
}

type inspectRequest struct {
	Containers []string `json:"containers"`
}

// InspectContainer returns the state of a single container by name.
func InspectContainer(name string) (model.ContainerState, error) {
	if agentClient == nil {
		return model.ContainerState{Name: name, Status: "unknown", Health: "unknown"}, nil
	}

	states := agentClient.Inspect([]string{name})
	if len(states) > 0 {
		return states[0], nil
	}
	return model.ContainerState{Name: name, Status: "unknown", Health: "unknown"}, nil
}

// InspectContainers returns the state of multiple containers.
func InspectContainers(names []string) []model.ContainerState {
	if agentClient == nil {
		states := make([]model.ContainerState, len(names))
		for i, name := range names {
			states[i] = model.ContainerState{Name: name, Status: "unknown", Health: "unknown"}
		}
		return states
	}
	return agentClient.Inspect(names)
}

// ContainerResourceStats holds per-container resource usage from docker stats.
type ContainerResourceStats struct {
	Name       string  `json:"name"`
	CPUPercent float64 `json:"cpu_percent"`
	MemUsageMB float64 `json:"mem_usage_mb"`
	MemLimitMB float64 `json:"mem_limit_mb"`
	NetRxBytes int64   `json:"net_rx_bytes"`
	NetTxBytes int64   `json:"net_tx_bytes"`
}

// Stats returns resource usage for all allowed containers via the agent.
func Stats() ([]ContainerResourceStats, error) {
	if agentClient == nil {
		return nil, nil
	}
	return agentClient.stats()
}

func (ai *AgentInspector) stats() ([]ContainerResourceStats, error) {
	resp, err := ai.httpClient.Get(ai.agentURL + "/v1/stats")
	if err != nil {
		slog.Error("agent stats", "err", err)
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("agent stats: HTTP %d", resp.StatusCode)
	}

	var stats []ContainerResourceStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("agent stats decode: %w", err)
	}
	return stats, nil
}

func (ai *AgentInspector) Inspect(names []string) []model.ContainerState {
	body, _ := json.Marshal(inspectRequest{Containers: names})
	resp, err := ai.httpClient.Post(ai.agentURL+"/v1/inspect", "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Error("agent inspect", "err", err)
		states := make([]model.ContainerState, len(names))
		for i, name := range names {
			states[i] = model.ContainerState{Name: name, Status: "unknown", Health: "unknown"}
		}
		return states
	}
	defer resp.Body.Close()

	var states []model.ContainerState
	if err := json.NewDecoder(resp.Body).Decode(&states); err != nil {
		slog.Error("agent inspect decode", "err", err)
		fallback := make([]model.ContainerState, len(names))
		for i, name := range names {
			fallback[i] = model.ContainerState{Name: name, Status: "unknown", Health: "unknown"}
		}
		return fallback
	}
	return states
}
