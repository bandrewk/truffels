package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os/exec"
	"time"
	"truffels-api/internal/model"
)

type inspectResult struct {
	State struct {
		Status     string `json:"Status"`
		StartedAt  string `json:"StartedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	Config       struct {
		Image string `json:"Image"`
	} `json:"Config"`
}

// InspectContainer returns the state of a single container by name.
func InspectContainer(name string) (model.ContainerState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .}}", name)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return model.ContainerState{
			Name:   name,
			Status: "not_found",
			Health: "unknown",
		}, nil
	}

	var ir inspectResult
	if err := json.Unmarshal(out.Bytes(), &ir); err != nil {
		slog.Error("parse inspect", "container", name, "err", err)
		return model.ContainerState{Name: name, Status: "unknown", Health: "unknown"}, nil
	}

	cs := model.ContainerState{
		Name:         name,
		Status:       ir.State.Status,
		RestartCount: ir.RestartCount,
		StartedAt:    ir.State.StartedAt,
		Image:        ir.Config.Image,
	}
	if ir.State.Health != nil {
		cs.Health = ir.State.Health.Status
	} else {
		cs.Health = "" // no healthcheck configured
	}
	return cs, nil
}

// InspectContainers returns the state of multiple containers.
func InspectContainers(names []string) []model.ContainerState {
	states := make([]model.ContainerState, 0, len(names))
	for _, name := range names {
		cs, _ := InspectContainer(name)
		states = append(states, cs)
	}
	return states
}
