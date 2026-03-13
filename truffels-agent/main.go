package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Allowlisted service IDs and their compose directory names.
var allowedServices = map[string]string{
	"bitcoind":        "bitcoin",
	"electrs":         "electrs",
	"ckpool":          "ckpool",
	"mempool":         "mempool",
	"ckstats":         "ckstats",
	"proxy":           "proxy",
	"mempool-db":      "mempool",
	"ckstats-db":      "ckstats",
	"truffels-agent":  "truffels",
	"truffels-api":    "truffels",
	"truffels-web":    "truffels",
}

// Allowlisted container names for inspection.
var allowedContainers = map[string]bool{
	"truffels-bitcoind":          true,
	"truffels-electrs":           true,
	"truffels-ckpool":            true,
	"truffels-mempool-backend":   true,
	"truffels-mempool-frontend":  true,
	"truffels-mempool-db":        true,
	"truffels-ckstats":           true,
	"truffels-ckstats-cron":      true,
	"truffels-ckstats-db":        true,
	"truffels-proxy":             true,
	"truffels-agent":             true,
	"truffels-api":               true,
	"truffels-web":               true,
}

var composeRoot string

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	composeRoot = envOr("TRUFFELS_COMPOSE_ROOT", "/srv/truffels/compose")
	listen := envOr("TRUFFELS_AGENT_LISTEN", ":9090")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/compose/up", handleComposeUp)
	mux.HandleFunc("POST /v1/compose/down", handleComposeDown)
	mux.HandleFunc("POST /v1/compose/stop", handleComposeStop)
	mux.HandleFunc("POST /v1/compose/restart", handleComposeRestart)
	mux.HandleFunc("POST /v1/compose/logs", handleComposeLogs)
	mux.HandleFunc("POST /v1/inspect", handleInspect)
	mux.HandleFunc("POST /v1/image/pull", handleImagePull)
	mux.HandleFunc("POST /v1/image/inspect", handleImageInspect)
	mux.HandleFunc("POST /v1/compose/build", handleComposeBuild)
	mux.HandleFunc("GET /v1/stats", handleStats)
	mux.HandleFunc("GET /v1/health", handleHealth)
	mux.HandleFunc("POST /v1/system/shutdown", handleSystemShutdown)
	mux.HandleFunc("POST /v1/system/restart", handleSystemRestart)

	srv := &http.Server{Addr: listen, Handler: mux}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		slog.Info("shutting down")
		srv.Close()
	}()

	slog.Info("starting truffels-agent", "listen", listen)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// --- Request/Response types ---

type serviceRequest struct {
	ServiceID string `json:"service_id"`
}

type logsRequest struct {
	ServiceID string `json:"service_id"`
	Tail      int    `json:"tail"`
}

type inspectRequest struct {
	Containers []string `json:"containers"`
}

type containerState struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	Health       string `json:"health"`
	RestartCount int    `json:"restart_count"`
	StartedAt    string `json:"started_at"`
	Image        string `json:"image"`
}

type inspectResult struct {
	State struct {
		Status    string `json:"Status"`
		StartedAt string `json:"StartedAt"`
		Health    *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	Config       struct {
		Image string `json:"Image"`
	} `json:"Config"`
}

// --- Handlers ---

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handleComposeUp(w http.ResponseWriter, r *http.Request) {
	var req serviceRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	dir := composeDir(req.ServiceID)
	if err := runCompose(dir, "up", "-d", "--remove-orphans"); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handleComposeDown(w http.ResponseWriter, r *http.Request) {
	var req serviceRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	dir := composeDir(req.ServiceID)
	if err := runCompose(dir, "down"); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handleComposeStop(w http.ResponseWriter, r *http.Request) {
	var req serviceRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	dir := composeDir(req.ServiceID)
	if err := runCompose(dir, "stop"); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handleComposeRestart(w http.ResponseWriter, r *http.Request) {
	var req serviceRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	dir := composeDir(req.ServiceID)
	if err := runCompose(dir, "restart"); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handleComposeLogs(w http.ResponseWriter, r *http.Request) {
	var req logsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if _, ok := allowedServices[req.ServiceID]; !ok {
		writeJSON(w, 403, map[string]string{"error": "service not allowed: " + req.ServiceID})
		return
	}
	if req.Tail <= 0 || req.Tail > 1000 {
		req.Tail = 200
	}

	dir := composeDir(req.ServiceID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "compose", "-f",
		dir+"/docker-compose.yml", "logs", "--tail",
		strconv.Itoa(req.Tail), "--no-color")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error(), "logs": out.String()})
		return
	}
	writeJSON(w, 200, map[string]string{"logs": out.String()})
}

func handleInspect(w http.ResponseWriter, r *http.Request) {
	var req inspectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	states := make([]containerState, 0, len(req.Containers))
	for _, name := range req.Containers {
		if !allowedContainers[name] {
			slog.Warn("inspect denied", "container", name)
			states = append(states, containerState{Name: name, Status: "denied", Health: "unknown"})
			continue
		}
		states = append(states, inspectContainer(name))
	}

	writeJSON(w, 200, states)
}

func inspectContainer(name string) containerState {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .}}", name)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return containerState{Name: name, Status: "not_found", Health: "unknown"}
	}

	var ir inspectResult
	if err := json.Unmarshal(out.Bytes(), &ir); err != nil {
		slog.Error("parse inspect", "container", name, "err", err)
		return containerState{Name: name, Status: "unknown", Health: "unknown"}
	}

	cs := containerState{
		Name:         name,
		Status:       ir.State.Status,
		RestartCount: ir.RestartCount,
		StartedAt:    ir.State.StartedAt,
		Image:        ir.Config.Image,
	}
	if ir.State.Health != nil && ir.State.Status == "running" {
		cs.Health = ir.State.Health.Status
	}
	return cs
}

// --- Image/Build Handlers ---

type imagePullRequest struct {
	Image string `json:"image"`
}

func handleImagePull(w http.ResponseWriter, r *http.Request) {
	var req imagePullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if req.Image == "" {
		writeJSON(w, 400, map[string]string{"error": "image required"})
		return
	}

	slog.Info("pulling image", "image", req.Image)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "pull", req.Image)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error(), "output": out.String()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok", "output": out.String()})
}

type imageInspectRequest struct {
	Container string `json:"container"`
}

type imageInspectResult struct {
	Image   string   `json:"image"`
	Digest  string   `json:"digest"`
	Tags    []string `json:"tags"`
}

func handleImageInspect(w http.ResponseWriter, r *http.Request) {
	var req imageInspectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	if !allowedContainers[req.Container] {
		writeJSON(w, 403, map[string]string{"error": "container not allowed"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get image name from container
	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format",
		"{{.Config.Image}}", req.Container)
	var imageOut bytes.Buffer
	cmd.Stdout = &imageOut
	if err := cmd.Run(); err != nil {
		writeJSON(w, 500, map[string]string{"error": "cannot inspect container: " + err.Error()})
		return
	}
	imageName := strings.TrimSpace(imageOut.String())

	// Get image digest
	cmd2 := exec.CommandContext(ctx, "docker", "inspect", "--format",
		"{{index .RepoDigests 0}}", imageName)
	var digestOut bytes.Buffer
	cmd2.Stdout = &digestOut
	cmd2.Stderr = &digestOut
	digest := ""
	if err := cmd2.Run(); err == nil {
		digest = strings.TrimSpace(digestOut.String())
		// Extract just the digest part after @
		if idx := strings.Index(digest, "@"); idx >= 0 {
			digest = digest[idx+1:]
		}
	}

	// Get tags
	cmd3 := exec.CommandContext(ctx, "docker", "inspect", "--format",
		"{{json .RepoTags}}", imageName)
	var tagsOut bytes.Buffer
	cmd3.Stdout = &tagsOut
	var tags []string
	if cmd3.Run() == nil {
		json.Unmarshal(bytes.TrimSpace(tagsOut.Bytes()), &tags)
	}

	writeJSON(w, 200, imageInspectResult{
		Image:  imageName,
		Digest: digest,
		Tags:   tags,
	})
}

// --- Container Stats ---

type containerStats struct {
	Name           string  `json:"name"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemUsageMB     float64 `json:"mem_usage_mb"`
	MemLimitMB     float64 `json:"mem_limit_mb"`
	NetRxBytes     int64   `json:"net_rx_bytes"`
	NetTxBytes     int64   `json:"net_tx_bytes"`
	BlockReadBytes  int64  `json:"block_read_bytes"`
	BlockWriteBytes int64  `json:"block_write_bytes"`
}

type dockerStatsJSON struct {
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	// Collect names of all allowed containers
	names := make([]string, 0, len(allowedContainers))
	for name := range allowedContainers {
		names = append(names, name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	args := append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, names...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		slog.Error("docker stats", "err", err)
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	var results []containerStats
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var ds dockerStatsJSON
		if err := json.Unmarshal([]byte(line), &ds); err != nil {
			slog.Error("parse stats line", "err", err, "line", line)
			continue
		}

		cs := containerStats{Name: ds.Name}
		cs.CPUPercent = parsePercent(ds.CPUPerc)
		cs.MemUsageMB, cs.MemLimitMB = parseMemUsage(ds.MemUsage)
		cs.NetRxBytes, cs.NetTxBytes = parseNetIO(ds.NetIO)
		cs.BlockReadBytes, cs.BlockWriteBytes = parseNetIO(ds.BlockIO) // same "X / Y" format
		results = append(results, cs)
	}

	writeJSON(w, 200, results)
}

// parsePercent parses "65.71%" to 65.71
func parsePercent(s string) float64 {
	s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// parseBytes parses human-readable byte values like "909MB", "2.083GiB", "7.09kB"
func parseBytes(s string) float64 {
	s = strings.TrimSpace(s)
	multipliers := []struct {
		suffix string
		mult   float64
	}{
		{"TiB", 1024 * 1024 * 1024 * 1024},
		{"GiB", 1024 * 1024 * 1024},
		{"MiB", 1024 * 1024},
		{"KiB", 1024},
		{"TB", 1e12},
		{"GB", 1e9},
		{"MB", 1e6},
		{"kB", 1e3},
		{"B", 1},
	}
	for _, m := range multipliers {
		if strings.HasSuffix(s, m.suffix) {
			numStr := strings.TrimSpace(strings.TrimSuffix(s, m.suffix))
			v, _ := strconv.ParseFloat(numStr, 64)
			return v * m.mult
		}
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// parseMemUsage parses "2.083GiB / 3.418GiB" to (usage_mb, limit_mb)
func parseMemUsage(s string) (float64, float64) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	usageBytes := parseBytes(parts[0])
	limitBytes := parseBytes(parts[1])
	return usageBytes / (1024 * 1024), limitBytes / (1024 * 1024)
}

// parseNetIO parses "909MB / 30.7GB" to (rx_bytes, tx_bytes)
func parseNetIO(s string) (int64, int64) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return int64(parseBytes(parts[0])), int64(parseBytes(parts[1]))
}

func handleComposeBuild(w http.ResponseWriter, r *http.Request) {
	var req serviceRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	dir := composeDir(req.ServiceID)
	slog.Info("building service", "service", req.ServiceID, "dir", dir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "compose", "-f",
		dir+"/docker-compose.yml", "build", "--no-cache")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error(), "output": out.String()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok", "output": out.String()})
}

// --- Helpers ---

func composeDir(serviceID string) string {
	dirName := allowedServices[serviceID]
	return composeRoot + "/" + dirName
}

func decodeAndValidate(w http.ResponseWriter, r *http.Request, req *serviceRequest) bool {
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return false
	}
	if _, ok := allowedServices[req.ServiceID]; !ok {
		writeJSON(w, 403, map[string]string{"error": "service not allowed: " + req.ServiceID})
		return false
	}
	slog.Info("agent action", "action", strings.TrimPrefix(r.URL.Path, "/v1/compose/"), "service", req.ServiceID)
	return true
}

func runCompose(composeDir string, args ...string) error {
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

func handleSystemShutdown(w http.ResponseWriter, r *http.Request) {
	slog.Warn("system shutdown requested")
	cmd := exec.Command("nsenter", "-t", "1", "-m", "--", "/sbin/shutdown", "-h", "now")
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Error("system shutdown failed", "err", err, "output", string(out))
		writeJSON(w, 500, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	slog.Warn("system restart requested")
	cmd := exec.Command("nsenter", "-t", "1", "-m", "--", "/sbin/reboot")
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Error("system restart failed", "err", err, "output", string(out))
		writeJSON(w, 500, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
