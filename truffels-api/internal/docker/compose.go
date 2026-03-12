package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type ComposeClient struct {
	agentURL   string
	httpClient *http.Client
}

func NewComposeClient(agentURL string) *ComposeClient {
	return &ComposeClient{
		agentURL: agentURL,
		httpClient: &http.Client{
			Timeout: 3 * time.Minute,
		},
	}
}

type agentServiceReq struct {
	ServiceID string `json:"service_id"`
}

type agentLogsReq struct {
	ServiceID string `json:"service_id"`
	Tail      int    `json:"tail"`
}

type agentResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Logs   string `json:"logs"`
	Output string `json:"output"`
}

type ImageInfo struct {
	Image  string   `json:"image"`
	Digest string   `json:"digest"`
	Tags   []string `json:"tags"`
}

func (c *ComposeClient) Up(serviceID string) error {
	return c.composeAction("/v1/compose/up", serviceID)
}

func (c *ComposeClient) Down(serviceID string) error {
	return c.composeAction("/v1/compose/down", serviceID)
}

func (c *ComposeClient) Restart(serviceID string) error {
	return c.composeAction("/v1/compose/restart", serviceID)
}

func (c *ComposeClient) Logs(serviceID string, tail int) (string, error) {
	body, _ := json.Marshal(agentLogsReq{ServiceID: serviceID, Tail: tail})
	resp, err := c.httpClient.Post(c.agentURL+"/v1/compose/logs", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("agent logs: %w", err)
	}
	defer resp.Body.Close()

	var ar agentResponse
	json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return ar.Logs, fmt.Errorf("agent logs: %s", ar.Error)
	}
	return ar.Logs, nil
}

// Pull pulls a Docker image via the agent. Returns the docker pull output.
func (c *ComposeClient) Pull(image string) (string, error) {
	body, _ := json.Marshal(map[string]string{"image": image})
	slog.Info("agent pull", "image", image)

	longClient := &http.Client{Timeout: 10 * time.Minute}
	resp, err := longClient.Post(c.agentURL+"/v1/image/pull", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("agent pull: %w", err)
	}
	defer resp.Body.Close()

	var ar agentResponse
	json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("agent pull: %s", ar.Error)
	}
	return ar.Output, nil
}

// ImageInspect returns image info for a running container via the agent.
func (c *ComposeClient) ImageInspect(container string) (*ImageInfo, error) {
	body, _ := json.Marshal(map[string]string{"container": container})

	resp, err := c.httpClient.Post(c.agentURL+"/v1/image/inspect", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("agent image inspect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		var ar agentResponse
		json.NewDecoder(resp.Body).Decode(&ar)
		return nil, fmt.Errorf("agent image inspect: %s", ar.Error)
	}

	var info ImageInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("agent image inspect decode: %w", err)
	}
	return &info, nil
}

// Build runs docker compose build for a service via the agent.
func (c *ComposeClient) Build(serviceID string) error {
	body, _ := json.Marshal(agentServiceReq{ServiceID: serviceID})
	slog.Info("agent build", "service", serviceID)

	longClient := &http.Client{Timeout: 10 * time.Minute}
	resp, err := longClient.Post(c.agentURL+"/v1/compose/build", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent build: %w", err)
	}
	defer resp.Body.Close()

	var ar agentResponse
	json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent build: %s", ar.Error)
	}
	return nil
}

// SystemAction sends a shutdown or restart command to the agent.
func (c *ComposeClient) SystemAction(action string) error {
	body, _ := json.Marshal(map[string]string{"action": action})
	resp, err := c.httpClient.Post(c.agentURL+"/v1/system/"+action, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent system %s: %w", action, err)
	}
	defer resp.Body.Close()

	var ar agentResponse
	json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent system %s: %s", action, ar.Error)
	}
	return nil
}

func (c *ComposeClient) composeAction(path, serviceID string) error {
	body, _ := json.Marshal(agentServiceReq{ServiceID: serviceID})
	slog.Info("agent request", "path", path, "service", serviceID)

	resp, err := c.httpClient.Post(c.agentURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent %s: %w", path, err)
	}
	defer resp.Body.Close()

	var ar agentResponse
	json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent %s: %s", path, ar.Error)
	}
	return nil
}
