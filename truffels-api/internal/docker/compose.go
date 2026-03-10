package docker

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

type ComposeClient struct{}

func NewComposeClient() *ComposeClient {
	return &ComposeClient{}
}

func (c *ComposeClient) Up(composeDir string) error {
	return c.run(composeDir, "up", "-d", "--remove-orphans")
}

func (c *ComposeClient) Down(composeDir string) error {
	return c.run(composeDir, "down")
}

func (c *ComposeClient) Restart(composeDir string) error {
	return c.run(composeDir, "restart")
}

func (c *ComposeClient) Logs(composeDir string, tail int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "compose", "-f",
		composeDir+"/docker-compose.yml", "logs", "--tail",
		fmt.Sprintf("%d", tail), "--no-color")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func (c *ComposeClient) run(composeDir string, args ...string) error {
	fullArgs := append([]string{"compose", "-f", composeDir + "/docker-compose.yml"}, args...)
	slog.Info("docker compose", "dir", composeDir, "args", strings.Join(args, " "))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", fullArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return nil
}
