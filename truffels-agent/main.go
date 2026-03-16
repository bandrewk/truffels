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
	"regexp"
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
	"truffels":        "truffels",
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

var version = "dev" // overridden via -ldflags "-X main.version=v0.2.0"

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
	mux.HandleFunc("POST /v1/system/journal", handleSystemJournal)
	mux.HandleFunc("GET /v1/system/info", handleSystemInfo)
	mux.HandleFunc("GET /v1/system/tuning", handleSystemTuningGet)
	mux.HandleFunc("POST /v1/system/tuning", handleSystemTuningSet)
	mux.HandleFunc("POST /v1/git/checkout", handleGitCheckout)
	mux.HandleFunc("POST /v1/compose/up-detached", handleComposeUpDetached)
	mux.HandleFunc("POST /v1/compose/rewrite-tags", handleComposeRewriteTags)

	srv := &http.Server{Addr: listen, Handler: mux}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh
		slog.Info("shutting down")
		_ = srv.Close()
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
	Since     string `json:"since,omitempty"`
	Container string `json:"container,omitempty"`
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
	writeJSON(w, 200, map[string]string{"status": "ok", "version": version})
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

	// If a specific container is requested, use docker logs directly
	if req.Container != "" {
		if !allowedContainers[req.Container] {
			writeJSON(w, 403, map[string]string{"error": "container not allowed: " + req.Container})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		shellCmd := fmt.Sprintf("docker logs --tail %d %s 2>&1 | tail -c 65536", req.Tail, req.Container)
		if req.Since != "" {
			shellCmd = fmt.Sprintf("docker logs --tail %d --since %s %s 2>&1 | tail -c 65536", req.Tail, req.Since, req.Container)
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", shellCmd)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		cleaned := stripANSI(out.String())
		writeJSON(w, 200, map[string]string{"logs": cleaned})
		return
	}

	dir := composeDir(req.ServiceID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	args := []string{"compose", "-f", dir + "/docker-compose.yml", "logs",
		"--tail", strconv.Itoa(req.Tail), "--no-color"}
	if req.Since != "" {
		args = append(args, "--since", req.Since)
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	cleaned := stripANSI(out.String())

	// If compose logs returned empty, fall back to docker logs for each container.
	// This handles services like ckpool whose \r-heavy output breaks --tail.
	if cleaned == "" && err == nil {
		cleaned = fallbackContainerLogs(ctx, req.ServiceID, req.Tail, req.Since)
	}

	if err != nil && cleaned == "" {
		writeJSON(w, 500, map[string]string{"error": err.Error(), "logs": cleaned})
		return
	}
	writeJSON(w, 200, map[string]string{"logs": cleaned})
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
		_ = json.Unmarshal(bytes.TrimSpace(tagsOut.Bytes()), &tags)
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

type buildRequest struct {
	ServiceID string            `json:"service_id"`
	BuildArgs map[string]string `json:"build_args,omitempty"`
}

func handleComposeBuild(w http.ResponseWriter, r *http.Request) {
	var req buildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if _, ok := allowedServices[req.ServiceID]; !ok {
		writeJSON(w, 403, map[string]string{"error": "service not allowed: " + req.ServiceID})
		return
	}
	slog.Info("agent action", "action", "build", "service", req.ServiceID)

	dir := composeDir(req.ServiceID)
	slog.Info("building service", "service", req.ServiceID, "dir", dir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	dockerArgs := []string{"docker", "compose", "-f", dir + "/docker-compose.yml", "build", "--no-cache"}
	for k, v := range req.BuildArgs {
		dockerArgs = append(dockerArgs, "--build-arg", k+"="+v)
	}
	// Run via nsenter so build-context paths resolve on the host filesystem
	nsArgs := append([]string{"-t", "1", "-m", "--"}, dockerArgs...)
	cmd := exec.CommandContext(ctx, "nsenter", nsArgs...)
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

// --- System Info ---

type systemInfoResponse struct {
	Hostname string           `json:"hostname"`
	OS       string           `json:"os"`
	Kernel   string           `json:"kernel"`
	Model    string           `json:"model"`
	CPUCores int              `json:"cpu_cores"`
	MemTotal string           `json:"mem_total"`
	MemFree  string           `json:"mem_free"`
	Uptime   string           `json:"uptime"`
	Networks []networkIfInfo  `json:"networks"`
	Storage  []storageInfo    `json:"storage"`
}

type networkIfInfo struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	MAC  string `json:"mac"`
}

type storageInfo struct {
	Device string `json:"device"`
	Mount  string `json:"mount"`
	FSType string `json:"fstype"`
	Size   string `json:"size"`
	Used   string `json:"used"`
	Free   string `json:"free"`
	UsePct string `json:"use_pct"`
}

func handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// nsrun enters mount namespace (filesystem access)
	nsrun := func(args ...string) string {
		a := append([]string{"-t", "1", "-m", "--"}, args...)
		cmd := exec.CommandContext(ctx, "nsenter", a...)
		var out bytes.Buffer
		cmd.Stdout = &out
		_ = cmd.Run()
		return strings.TrimSpace(out.String())
	}

	// nsrunAll enters mount + UTS + network namespaces
	nsrunAll := func(args ...string) string {
		a := append([]string{"-t", "1", "-m", "-u", "-n", "--"}, args...)
		cmd := exec.CommandContext(ctx, "nsenter", a...)
		var out bytes.Buffer
		cmd.Stdout = &out
		_ = cmd.Run()
		return strings.TrimSpace(out.String())
	}

	// Hostname (needs UTS namespace)
	hostname := nsrunAll("hostname")

	// OS pretty name from /etc/os-release
	osRelease := nsrun("sh", "-c", "grep ^PRETTY_NAME /etc/os-release | cut -d= -f2 | tr -d '\"'")

	// Kernel
	kernel := nsrun("uname", "-r")

	// Device model (strip null byte from device tree)
	model := strings.ReplaceAll(nsrun("cat", "/sys/firmware/devicetree/base/model"), "\x00", "")

	// CPU cores
	cpuStr := nsrun("nproc")
	cpuCores, _ := strconv.Atoi(cpuStr)

	// Memory: total and available from /proc/meminfo
	memTotal := nsrun("sh", "-c", "awk '/^MemTotal:/{printf \"%.0f MB\", $2/1024}' /proc/meminfo")
	memFree := nsrun("sh", "-c", "awk '/^MemAvailable:/{printf \"%.0f MB\", $2/1024}' /proc/meminfo")

	// Uptime
	uptimeRaw := nsrun("cat", "/proc/uptime")
	var uptime string
	if fields := strings.Fields(uptimeRaw); len(fields) > 0 {
		if secs, err := strconv.ParseFloat(fields[0], 64); err == nil {
			d := int(secs) / 86400
			h := (int(secs) % 86400) / 3600
			m := (int(secs) % 3600) / 60
			if d > 0 {
				uptime = fmt.Sprintf("%dd %dh %dm", d, h, m)
			} else if h > 0 {
				uptime = fmt.Sprintf("%dh %dm", h, m)
			} else {
				uptime = fmt.Sprintf("%dm", m)
			}
		}
	}

	// Network interfaces — needs network namespace (skip lo and docker/veth)
	ipOut := nsrunAll("sh", "-c", "ip -o addr show | awk '{print $2, $4, $NF}'")
	var networks []networkIfInfo
	seen := map[string]bool{}
	for _, line := range strings.Split(ipOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		iface := fields[0]
		if iface == "lo" || strings.HasPrefix(iface, "veth") || strings.HasPrefix(iface, "br-") || strings.HasPrefix(iface, "docker") {
			continue
		}
		addr := fields[1]
		// Skip IPv6 link-local
		if strings.Contains(addr, ":") {
			continue
		}
		if seen[iface] {
			continue
		}
		seen[iface] = true
		// Get MAC — needs network namespace for host interfaces
		mac := nsrunAll("sh", "-c", fmt.Sprintf("cat /sys/class/net/%s/address", iface))
		networks = append(networks, networkIfInfo{
			Name: iface,
			IP:   addr,
			MAC:  mac,
		})
	}

	// Storage — df for real filesystems (skip tmpfs, devtmpfs, overlay, etc.)
	dfOut := nsrun("sh", "-c", "df -hT | awk 'NR>1 && $2!=\"tmpfs\" && $2!=\"devtmpfs\" && $2!=\"overlay\" && $2!=\"squashfs\" {print $1, $7, $2, $3, $4, $5, $6}'")
	var storage []storageInfo
	for _, line := range strings.Split(dfOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		storage = append(storage, storageInfo{
			Device: fields[0],
			Mount:  fields[1],
			FSType: fields[2],
			Size:   fields[3],
			Used:   fields[4],
			Free:   fields[5],
			UsePct: fields[6],
		})
	}

	writeJSON(w, 200, systemInfoResponse{
		Hostname: hostname,
		OS:       osRelease,
		Kernel:   kernel,
		Model:    model,
		CPUCores: cpuCores,
		MemTotal: memTotal,
		MemFree:  memFree,
		Uptime:   uptime,
		Networks: networks,
		Storage:  storage,
	})
}

// --- System Journal & Tuning ---

type journalRequest struct {
	Lines    int    `json:"lines"`
	Priority string `json:"priority"`
	Unit     string `json:"unit"`
	Since    string `json:"since"`
	Boot     int    `json:"boot"`
}

var allowedPriorities = map[string]bool{
	"": true, "emerg": true, "crit": true, "err": true,
	"warning": true, "info": true, "debug": true,
}

var allowedUnits = map[string]bool{
	"": true, "docker": true, "kernel": true, "systemd": true,
	"nftables": true, "ssh": true,
}

func handleSystemJournal(w http.ResponseWriter, r *http.Request) {
	var req journalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	if !allowedPriorities[req.Priority] {
		writeJSON(w, 400, map[string]string{"error": "invalid priority"})
		return
	}
	if !allowedUnits[req.Unit] {
		writeJSON(w, 400, map[string]string{"error": "invalid unit"})
		return
	}
	if req.Boot > 0 {
		writeJSON(w, 400, map[string]string{"error": "boot must be 0 or negative"})
		return
	}
	if req.Lines <= 0 || req.Lines > 1000 {
		req.Lines = 200
	}

	args := []string{"-t", "1", "-m", "--", "journalctl", "--no-pager",
		"--output=short", "-n", strconv.Itoa(req.Lines)}
	args = append(args, "-b", strconv.Itoa(req.Boot))
	if req.Priority != "" {
		args = append(args, "-p", req.Priority)
	}
	if req.Unit != "" {
		if req.Unit == "kernel" {
			args = append(args, "-k")
		} else {
			args = append(args, "-u", req.Unit)
		}
	}
	if req.Since != "" {
		args = append(args, "--since", req.Since)
	}

	slog.Info("system journal", "lines", req.Lines, "priority", req.Priority, "unit", req.Unit)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nsenter", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// journalctl exits 1 when boot not found — return empty logs, not an error
		text := out.String()
		if strings.Contains(text, "no persistent journal") || strings.Contains(text, "No boot ID matched") || strings.Contains(text, "No journal boot entry found") {
			writeJSON(w, 200, map[string]string{"logs": ""})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error(), "logs": text})
		return
	}
	writeJSON(w, 200, map[string]string{"logs": out.String()})
}

type bootEntry struct {
	Index int    `json:"index"`
	ID    string `json:"id"`
	First string `json:"first"`
	Last  string `json:"last"`
}

type tuningResponse struct {
	PersistentJournal bool        `json:"persistent_journal"`
	Swappiness        int         `json:"swappiness"`
	JournalDiskUsage  string      `json:"journal_disk_usage"`
	Boots             []bootEntry `json:"boots"`
}

func handleSystemTuningGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check if persistent journal is configured via drop-in
	cmd1 := exec.CommandContext(ctx, "nsenter", "-t", "1", "-m", "--",
		"test", "-f", "/etc/systemd/journald.conf.d/truffels.conf")
	persistent := cmd1.Run() == nil

	// Read swappiness
	cmd2 := exec.CommandContext(ctx, "nsenter", "-t", "1", "-m", "--",
		"cat", "/proc/sys/vm/swappiness")
	var swapOut bytes.Buffer
	cmd2.Stdout = &swapOut
	_ = cmd2.Run()
	swappiness, _ := strconv.Atoi(strings.TrimSpace(swapOut.String()))

	// Journal disk usage
	cmd3 := exec.CommandContext(ctx, "nsenter", "-t", "1", "-m", "--",
		"journalctl", "--disk-usage")
	var usageOut bytes.Buffer
	cmd3.Stdout = &usageOut
	_ = cmd3.Run()
	usage := strings.TrimSpace(usageOut.String())
	// Extract just the size part, e.g. "Archived and active journals take up 8.0M in the file system."
	if idx := strings.Index(usage, "take up "); idx >= 0 {
		rest := usage[idx+8:]
		if end := strings.Index(rest, " "); end >= 0 {
			usage = rest[:end]
		}
	}

	// List available boots
	cmd4 := exec.CommandContext(ctx, "nsenter", "-t", "1", "-m", "--",
		"journalctl", "--list-boots", "--no-pager")
	var bootsOut bytes.Buffer
	cmd4.Stdout = &bootsOut
	_ = cmd4.Run()
	var boots []bootEntry
	for _, line := range strings.Split(bootsOut.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "IDX") {
			continue
		}
		// Format: IDX UUID DOW DATE TIME TZ DOW DATE TIME TZ
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		idx, _ := strconv.Atoi(fields[0])
		boots = append(boots, bootEntry{
			Index: idx,
			ID:    fields[1],
			First: fields[3] + " " + fields[4],
			Last:  fields[7] + " " + fields[8],
		})
	}

	writeJSON(w, 200, tuningResponse{
		PersistentJournal: persistent,
		Swappiness:        swappiness,
		JournalDiskUsage:  usage,
		Boots:             boots,
	})
}

type tuningSetRequest struct {
	Action string `json:"action"`
	Value  string `json:"value"`
}

func handleSystemTuningSet(w http.ResponseWriter, r *http.Request) {
	var req tuningSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	switch req.Action {
	case "set_persistent_journal":
		if req.Value != "true" && req.Value != "false" {
			writeJSON(w, 400, map[string]string{"error": "value must be true or false"})
			return
		}
		if req.Value == "true" {
			// Create persistent journal dir, set Storage=persistent via drop-in, restart journald
			cmd := exec.CommandContext(ctx, "nsenter", "-t", "1", "-m", "--",
				"sh", "-c", "mkdir -p /var/log/journal && systemd-tmpfiles --create --prefix /var/log/journal && mkdir -p /etc/systemd/journald.conf.d && printf '[Journal]\\nStorage=persistent\\n' > /etc/systemd/journald.conf.d/truffels.conf && systemctl restart systemd-journald")
			if out, err := cmd.CombinedOutput(); err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error(), "output": string(out)})
				return
			}
		} else {
			// Remove persistent journal dir + drop-in config, restart journald
			cmd := exec.CommandContext(ctx, "nsenter", "-t", "1", "-m", "--",
				"sh", "-c", "rm -rf /var/log/journal && rm -f /etc/systemd/journald.conf.d/truffels.conf && systemctl restart systemd-journald")
			if out, err := cmd.CombinedOutput(); err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error(), "output": string(out)})
				return
			}
		}

	case "set_swappiness":
		val, err := strconv.Atoi(req.Value)
		if err != nil || val < 0 || val > 100 {
			writeJSON(w, 400, map[string]string{"error": "swappiness must be 0-100"})
			return
		}
		// Set live + persist to sysctl.d
		script := fmt.Sprintf(
			"sysctl -w vm.swappiness=%d && mkdir -p /etc/sysctl.d && echo 'vm.swappiness=%d' > /etc/sysctl.d/90-truffels.conf",
			val, val)
		cmd := exec.CommandContext(ctx, "nsenter", "-t", "1", "-m", "--", "sh", "-c", script)
		if out, err := cmd.CombinedOutput(); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error(), "output": string(out)})
			return
		}

	default:
		writeJSON(w, 400, map[string]string{"error": "unknown action"})
		return
	}

	slog.Info("system tuning applied", "action", req.Action, "value", req.Value)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// allowedRepoDir is the mounted project repo path inside the container.
const allowedRepoDir = "/repo"

type gitCheckoutRequest struct {
	RepoDir string `json:"repo_dir"`
	Tag     string `json:"tag"`
}

func handleGitCheckout(w http.ResponseWriter, r *http.Request) {
	var req gitCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	// Validate repo_dir is the allowed path
	if req.RepoDir != allowedRepoDir {
		writeJSON(w, 403, map[string]string{"error": "repo_dir not allowed"})
		return
	}
	if req.Tag == "" {
		writeJSON(w, 400, map[string]string{"error": "tag required"})
		return
	}
	// Validate tag format (must start with v and contain only semver chars)
	if !isValidTag(req.Tag) {
		writeJSON(w, 400, map[string]string{"error": "invalid tag format"})
		return
	}

	slog.Info("git checkout", "repo", req.RepoDir, "tag", req.Tag)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Fetch tags (safe.directory needed: agent runs as root, repo owned by uid 1000)
	fetchCmd := exec.CommandContext(ctx, "git", "-c", "safe.directory=*", "-C", req.RepoDir, "fetch", "--tags", "--force")
	var fetchOut bytes.Buffer
	fetchCmd.Stdout = &fetchOut
	fetchCmd.Stderr = &fetchOut
	if err := fetchCmd.Run(); err != nil {
		writeJSON(w, 500, map[string]string{"error": "git fetch failed: " + err.Error(), "output": fetchOut.String()})
		return
	}

	// Checkout tag
	checkoutCmd := exec.CommandContext(ctx, "git", "-c", "safe.directory=*", "-C", req.RepoDir, "checkout", req.Tag)
	var checkoutOut bytes.Buffer
	checkoutCmd.Stdout = &checkoutOut
	checkoutCmd.Stderr = &checkoutOut
	if err := checkoutCmd.Run(); err != nil {
		writeJSON(w, 500, map[string]string{"error": "git checkout failed: " + err.Error(), "output": checkoutOut.String()})
		return
	}

	writeJSON(w, 200, map[string]string{"status": "ok", "output": fetchOut.String() + checkoutOut.String()})
}

func isValidTag(tag string) bool {
	if len(tag) < 2 || tag[0] != 'v' {
		return false
	}
	for _, c := range tag[1:] {
		if c != '.' && c != '-' && (c < '0' || c > '9') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

type composeUpDetachedRequest struct {
	ServiceID string `json:"service_id"`
}

func handleComposeUpDetached(w http.ResponseWriter, r *http.Request) {
	var req composeUpDetachedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if _, ok := allowedServices[req.ServiceID]; !ok {
		writeJSON(w, 403, map[string]string{"error": "service not allowed: " + req.ServiceID})
		return
	}

	dir := composeDir(req.ServiceID)
	composePath := dir + "/docker-compose.yml"
	slog.Warn("detached compose up requested", "service", req.ServiceID, "compose", composePath)

	// Write a self-update script that runs on the host via nsenter.
	// The sleep ensures the HTTP response is sent before the agent container is replaced.
	script := fmt.Sprintf(`#!/bin/sh
sleep 2
docker compose -f %s up -d --remove-orphans
`, composePath)

	scriptPath := "/tmp/truffels-self-update.sh"
	// Write script via nsenter into host filesystem
	writeCmd := exec.Command("nsenter", "-t", "1", "-m", "--",
		"sh", "-c", fmt.Sprintf("cat > %s << 'SCRIPT'\n%sSCRIPT\nchmod +x %s", scriptPath, script, scriptPath))
	if out, err := writeCmd.CombinedOutput(); err != nil {
		writeJSON(w, 500, map[string]string{"error": "write script failed: " + err.Error(), "output": string(out)})
		return
	}

	// Execute detached via nsenter (runs on host, survives container replacement)
	execCmd := exec.Command("nsenter", "-t", "1", "-m", "-p", "--",
		"sh", "-c", fmt.Sprintf("nohup %s > /tmp/truffels-self-update.log 2>&1 &", scriptPath))
	if out, err := execCmd.CombinedOutput(); err != nil {
		writeJSON(w, 500, map[string]string{"error": "exec script failed: " + err.Error(), "output": string(out)})
		return
	}

	writeJSON(w, 202, map[string]string{"status": "accepted", "message": "detached compose up scheduled"})
}

// --- Compose Rewrite Tags ---

type rewriteTagsRequest struct {
	ServiceID string   `json:"service_id"`
	Images    []string `json:"images"`
	OldTag    string   `json:"old_tag"`
	NewTag    string   `json:"new_tag"`
}

func handleComposeRewriteTags(w http.ResponseWriter, r *http.Request) {
	var req rewriteTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if _, ok := allowedServices[req.ServiceID]; !ok {
		writeJSON(w, 403, map[string]string{"error": "service not allowed: " + req.ServiceID})
		return
	}
	if len(req.Images) == 0 || req.OldTag == "" || req.NewTag == "" {
		writeJSON(w, 400, map[string]string{"error": "images, old_tag, and new_tag are required"})
		return
	}

	slog.Info("rewrite compose tags", "service", req.ServiceID, "old", req.OldTag, "new", req.NewTag)

	composePath := composeDir(req.ServiceID) + "/docker-compose.yml"
	data, err := os.ReadFile(composePath)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "read compose file: " + err.Error()})
		return
	}

	content := string(data)
	for _, img := range req.Images {
		pattern := fmt.Sprintf(`(image:\s*)%s:%s(@sha256:[a-f0-9]+)?`, regexp.QuoteMeta(img), regexp.QuoteMeta(req.OldTag))
		re, err := regexp.Compile(pattern)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "compile regex for " + img + ": " + err.Error()})
			return
		}
		replacement := fmt.Sprintf("${1}%s:%s", img, req.NewTag)
		content = re.ReplaceAllString(content, replacement)
	}

	// Also rewrite VERSION build args (used by truffels self-update)
	content = strings.ReplaceAll(content, "VERSION: "+req.OldTag, "VERSION: "+req.NewTag)

	if content == string(data) {
		writeJSON(w, 400, map[string]string{"error": "no tags matched — compose file unchanged"})
		return
	}

	if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
		writeJSON(w, 500, map[string]string{"error": "write compose file: " + err.Error()})
		return
	}

	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// serviceContainers maps service IDs to their container names for fallback log retrieval.
var serviceContainers = map[string][]string{
	"bitcoind":        {"truffels-bitcoind"},
	"electrs":         {"truffels-electrs"},
	"ckpool":          {"truffels-ckpool"},
	"mempool":         {"truffels-mempool-backend", "truffels-mempool-frontend"},
	"ckstats":         {"truffels-ckstats", "truffels-ckstats-cron"},
	"proxy":           {"truffels-proxy"},
	"mempool-db":      {"truffels-mempool-db"},
	"ckstats-db":      {"truffels-ckstats-db"},
	"truffels-agent":  {"truffels-agent"},
	"truffels-api":    {"truffels-api"},
	"truffels-web":    {"truffels-web"},
}

// fallbackContainerLogs uses `docker logs` directly when `docker compose logs` returns empty.
// This handles containers whose \r-heavy output breaks compose's --tail.
func fallbackContainerLogs(ctx context.Context, serviceID string, tail int, since string) string {
	containers, ok := serviceContainers[serviceID]
	if !ok {
		return ""
	}

	var result strings.Builder
	for _, name := range containers {
		// For CR-based output (e.g. ckpool spinner), the entire output is one Docker log
		// entry with no newlines. --tail is useless and --since doesn't work (timestamp is
		// from the first write). Use shell pipe to grab only the last 64KB.
		shellCmd := fmt.Sprintf("docker logs %s 2>&1 | tail -c 65536", name)
		if since != "" {
			shellCmd = fmt.Sprintf("docker logs --since %s %s 2>&1 | tail -c 65536", since, name)
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", shellCmd)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			continue
		}
		cleaned := stripANSI(out.String())
		// Split on \n, take last N lines
		lines := strings.Split(strings.TrimRight(cleaned, "\n"), "\n")
		if len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		if len(containers) > 1 {
			// Prefix with container name for multi-container services
			for i, line := range lines {
				if line != "" {
					lines[i] = name + "  | " + line
				}
			}
		}
		result.WriteString(strings.Join(lines, "\n"))
		result.WriteString("\n")
	}
	return strings.TrimRight(result.String(), "\n")
}

// ansiPattern matches ANSI escape sequences (colors, cursor control, erase).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripANSI removes ANSI escape sequences and converts carriage returns to newlines.
// This handles programs like ckpool that use \x1B[2K\r (erase line + CR) for spinners,
// turning each spinner update into its own line.
func stripANSI(s string) string {
	s = ansiPattern.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n") // preserve real line endings
	s = strings.ReplaceAll(s, "\r", "\n")   // convert CR-only to newline
	// Clean up empty lines from the conversion
	for strings.Contains(s, "\n\n") {
		s = strings.ReplaceAll(s, "\n\n", "\n")
	}
	return strings.TrimLeft(s, "\n")
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
