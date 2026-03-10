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
