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
	"path/filepath"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
var dataRoot string
var configRoot string
var sizeCache *dirSizeCache
var storageCache *dockerStorageCache

var version = "dev" // overridden via -ldflags "-X main.version=v0.2.0"

// sizeSpaceRe matches the boundary between a digit (or dot) and a letter (unit suffix).
var sizeSpaceRe = regexp.MustCompile(`(\d)([A-Za-z])`)

// formatSize inserts a space between the numeric part and the unit suffix.
// "111.2GB" → "111.2 GB", "1.8T" → "1.8 T", "52%" → "52%" (percentages unchanged).
// Already-spaced values are returned as-is (idempotent).
func formatSize(s string) string {
	return sizeSpaceRe.ReplaceAllString(s, "$1 $2")
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	composeRoot = envOr("TRUFFELS_COMPOSE_ROOT", "/srv/truffels/compose")
	dataRoot = envOr("TRUFFELS_DATA_ROOT", "/srv/truffels/data")
	configRoot = envOr("TRUFFELS_CONFIG_ROOT", "/srv/truffels/config")
	listen := envOr("TRUFFELS_AGENT_LISTEN", ":9090")

	sizeCache = newDirSizeCache()
	go walkDataDirsForever(sizeCache, dataRoot)

	storageCache = newDockerStorageCache(5 * time.Minute)
	go walkDockerStorageForever(storageCache)

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
	mux.HandleFunc("GET /v1/host/dir-size", handleHostDirSize)
	mux.HandleFunc("POST /v1/system/shutdown", handleSystemShutdown)
	mux.HandleFunc("POST /v1/system/restart", handleSystemRestart)
	mux.HandleFunc("POST /v1/system/journal", handleSystemJournal)
	mux.HandleFunc("GET /v1/system/info", handleSystemInfo)
	mux.HandleFunc("GET /v1/system/tuning", handleSystemTuningGet)
	mux.HandleFunc("POST /v1/system/tuning", handleSystemTuningSet)
	mux.HandleFunc("POST /v1/git/checkout", handleGitCheckout)
	mux.HandleFunc("POST /v1/compose/up-detached", handleComposeUpDetached)
	mux.HandleFunc("POST /v1/compose/rewrite-tags", handleComposeRewriteTags)
	mux.HandleFunc("POST /v1/compose/read", handleComposeRead)
	mux.HandleFunc("POST /v1/compose/reconcile", handleComposeReconcile)
	mux.HandleFunc("POST /v1/file/reconcile", handleFileReconcile)
	mux.HandleFunc("POST /v1/fs/ensure-dir", handleEnsureDir)
	mux.HandleFunc("POST /v1/fs/clear-dir", handleClearDir)
	mux.HandleFunc("POST /v1/image/remove", handleImageRemove)
	mux.HandleFunc("POST /v1/docker/prune", handleDockerPrune)
	mux.HandleFunc("POST /v1/docker/prune-buildcache", handleDockerPruneBuildCache)

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

// handleHostDirSize returns the cached size of a single data-dir path.
//
// Reads from the same `sizeCache` populated by walkDataDirsForever (5-min
// cadence), so callers don't trigger a fresh walk. Path must be under
// dataRoot — anything else is rejected to keep the API a narrow shim over the
// existing cache, not a general "size any host path you want" oracle.
//
// Added in v0.3.1-dev.21 for the mempool cache-dir warn metric (700M / 900M
// thresholds) — see FUTURE_WORK.md and the dev.20 rbfcache OOM post-mortem.
func handleHostDirSize(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeJSON(w, 400, map[string]string{"error": "missing path"})
		return
	}
	clean := filepath.Clean(path)
	rootClean := filepath.Clean(dataRoot)
	if clean != rootClean && !strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		writeJSON(w, 400, map[string]string{"error": "path not under data root"})
		return
	}
	if sizeCache == nil {
		writeJSON(w, 200, map[string]interface{}{
			"path": clean, "size_bytes": int64(-1), "walked_at": "", "fresh": false,
		})
		return
	}
	size, walked, hit := sizeCache.get(clean)
	if !hit {
		writeJSON(w, 200, map[string]interface{}{
			"path": clean, "size_bytes": int64(-1), "walked_at": "", "fresh": false,
		})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"path":       clean,
		"size_bytes": size,
		"walked_at":  walked.UTC().Format(time.RFC3339),
		"fresh":      time.Since(walked) < time.Hour,
	})
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
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

// allowedImagePrefixes controls which images can be removed via /v1/image/remove.
var allowedImagePrefixes = []string{
	"truffels/", "mempool/", "btcpayserver/", "getumbrel/",
	"caddy:", "postgres:", "mariadb:",
}

func handleImageRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image string `json:"image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if req.Image == "" {
		writeJSON(w, 400, map[string]string{"error": "image required"})
		return
	}

	// Allowlist check
	allowed := false
	for _, prefix := range allowedImagePrefixes {
		if strings.HasPrefix(req.Image, prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		writeJSON(w, 403, map[string]string{"error": "image not allowed"})
		return
	}

	slog.Info("removing image", "image", req.Image)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "rmi", req.Image)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// Best-effort: log warning but return OK for "in use" or "not found" errors
		errMsg := out.String()
		slog.Warn("image remove failed (best-effort)", "image", req.Image, "err", errMsg)
		writeJSON(w, 200, map[string]string{"status": "ok", "warning": errMsg})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handleDockerPrune(w http.ResponseWriter, r *http.Request) {
	slog.Info("docker prune requested")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var totalReclaimed bytes.Buffer

	// Builder prune
	cmd := exec.CommandContext(ctx, "docker", "builder", "prune", "-a", "-f")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		slog.Warn("builder prune failed", "err", err)
	}
	totalReclaimed.WriteString(out.String())

	// Image prune
	out.Reset()
	cmd = exec.CommandContext(ctx, "docker", "image", "prune", "-a", "-f")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		slog.Warn("image prune failed", "err", err)
	}
	totalReclaimed.WriteString(out.String())

	// System prune
	out.Reset()
	cmd = exec.CommandContext(ctx, "docker", "system", "prune", "-f")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		slog.Warn("system prune failed", "err", err)
	}
	totalReclaimed.WriteString(out.String())

	// Extract reclaimed sizes from output
	reclaimed := "unknown"
	lines := strings.Split(totalReclaimed.String(), "\n")
	var reclaimedParts []string
	for _, line := range lines {
		if strings.Contains(line, "reclaimed") {
			reclaimedParts = append(reclaimedParts, strings.TrimSpace(line))
		}
	}
	if len(reclaimedParts) > 0 {
		reclaimed = strings.Join(reclaimedParts, "; ")
	}

	// Refresh storage immediately so /system/info reflects the prune result
	// on the very next request, not 5 minutes later.
	if storageCache != nil {
		storageCache.invalidate()
		storageCache.set(fetchDockerStorage())
	}

	writeJSON(w, 200, map[string]string{"status": "ok", "reclaimed": reclaimed})
}

func handleDockerPruneBuildCache(w http.ResponseWriter, r *http.Request) {
	slog.Info("docker build cache prune requested")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "builder", "prune", "-a", "-f")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		writeJSON(w, 500, map[string]string{"error": "builder prune failed: " + err.Error()})
		return
	}

	reclaimed := "unknown"
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "reclaimed") {
			reclaimed = strings.TrimSpace(line)
			break
		}
	}

	// Refresh storage — see handleDockerPrune for rationale.
	if storageCache != nil {
		storageCache.invalidate()
		storageCache.set(fetchDockerStorage())
	}

	writeJSON(w, 200, map[string]string{"status": "ok", "reclaimed": reclaimed})
}

type imageInspectRequest struct {
	Container string `json:"container"`
}

type imageInspectResponse struct {
	Image  string            `json:"image"`
	Digest string            `json:"digest"`
	Tags   []string          `json:"tags"`
	Labels map[string]string `json:"labels,omitempty"`
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

	// Get labels — carries org.truffels.source-ref, the ref the image was built from.
	cmd4 := exec.CommandContext(ctx, "docker", "inspect", "--format",
		"{{json .Config.Labels}}", imageName)
	var labelsOut bytes.Buffer
	cmd4.Stdout = &labelsOut
	labels := map[string]string{}
	if cmd4.Run() == nil {
		_ = json.Unmarshal(bytes.TrimSpace(labelsOut.Bytes()), &labels)
	}

	writeJSON(w, 200, imageInspectResponse{
		Image:  imageName,
		Digest: digest,
		Tags:   tags,
		Labels: labels,
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
	Services  []string          `json:"services,omitempty"` // compose service names to build (empty = all)
}

// composeBuildMaxAttempts is the number of times handleComposeBuild will
// re-run `docker compose build` if it fails. Tuned at 3 — with layer cache
// enabled (no --no-cache), retries resume from the cached layer just before
// the failed RUN step, so the cost of an extra attempt is low. Most transient
// network flakes (download.docker.com, deb.debian.org, registry.npmjs.org)
// are handled inside Dockerfiles via the retry shell function; this is the
// outer safety net for flakes that outlast the in-Dockerfile budget.
const composeBuildMaxAttempts = 3
const composeBuildRetrySleep = 30 * time.Second

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

	// 20 min total budget covers 3 build attempts + 2x 30s sleeps. With layer
	// cache enabled (--no-cache removed in dev.19), retries are typically
	// fast because successful layers stay cached.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	dockerArgs := []string{"docker", "compose", "-f", dir + "/docker-compose.yml", "build"}
	for k, v := range req.BuildArgs {
		dockerArgs = append(dockerArgs, "--build-arg", k+"="+v)
	}
	dockerArgs = append(dockerArgs, req.Services...)
	// Run via nsenter so build-context paths resolve on the host filesystem.
	nsArgs := append([]string{"-t", "1", "-m", "--"}, dockerArgs...)

	var lastErr error
	var lastOutput string
	for attempt := 1; attempt <= composeBuildMaxAttempts; attempt++ {
		var out bytes.Buffer
		cmd := exec.CommandContext(ctx, "nsenter", nsArgs...)
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		if err == nil {
			writeJSON(w, 200, map[string]string{"status": "ok", "output": out.String()})
			return
		}
		lastErr = err
		lastOutput = out.String()
		slog.Warn("compose build failed", "service", req.ServiceID, "attempt", attempt, "max", composeBuildMaxAttempts, "error", err.Error())
		if attempt < composeBuildMaxAttempts {
			select {
			case <-ctx.Done():
			case <-time.After(composeBuildRetrySleep):
			}
		}
	}
	writeJSON(w, 500, map[string]string{
		"error":    lastErr.Error(),
		"output":   lastOutput,
		"attempts": fmt.Sprint(composeBuildMaxAttempts),
	})
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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

type dockerStorageItem struct {
	Type           string `json:"type"`
	Count          int    `json:"count"`
	TotalSize      string `json:"total_size"`
	Reclaimable    string `json:"reclaimable"`
	ReclaimableRaw int64  `json:"reclaimable_raw"` // bytes — for client-side threshold gates
}

type systemInfoResponse struct {
	Hostname      string              `json:"hostname"`
	OS            string              `json:"os"`
	Kernel        string              `json:"kernel"`
	Model         string              `json:"model"`
	CPUCores      int                 `json:"cpu_cores"`
	MemTotal      string              `json:"mem_total"`
	MemFree       string              `json:"mem_free"`
	Uptime        string              `json:"uptime"`
	Networks      []networkIfInfo     `json:"networks"`
	Storage       []storageInfo       `json:"storage"`
	DockerStorage []dockerStorageItem `json:"docker_storage,omitempty"`
	ServiceData   []serviceDataItem   `json:"service_data,omitempty"`
}

type serviceDataItem struct {
	Path    string `json:"path"`
	Size    string `json:"size"`
	SizeRaw int64  `json:"size_raw"` // bytes — for client-side sorting/thresholds
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
			Size:   formatSize(fields[3]),
			Used:   formatSize(fields[4]),
			Free:   formatSize(fields[5]),
			UsePct: fields[6],
		})
	}

	// Docker storage: cache-only read. The 5-min background goroutine
	// (walkDockerStorageForever) refreshes the cache. On a pi with many
	// build-cache items `docker system df` takes 15+ seconds — running it
	// inline inside an HTTP handler caused the request to time out and the
	// cache to be populated with empty, hiding the Docker Storage card.
	// If the cache is empty during the first few seconds after startup,
	// the UI renders no card; the next refresh populates it.
	var dockerStorage []dockerStorageItem
	if storageCache != nil {
		dockerStorage, _ = storageCache.get()
	}

	// Service data: serve sizes from the background cache. Paths not yet
	// walked return "calculating..." with SizeRaw: -1 so the frontend can
	// render the row immediately and show a placeholder.
	//
	// We emit BOTH the top-level service path and any 2-level child dirs.
	// Templates declare data dirs at either granularity:
	//   - 1-level: ckpool (/srv/truffels/data/ckpool), truffels
	//   - 2-level: bitcoind (.../bitcoin/blockchain), electrs, mempool, ckstats
	// dev.18 only emitted child rows when child dirs existed, so 1-level
	// templates got a "—" in the UI. Frontend filters by template path so
	// extra rows are harmless.
	var serviceData []serviceDataItem
	if entries, err := os.ReadDir(dataRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			servicePath := filepath.Join(dataRoot, e.Name())
			addRow := func(full string) {
				if sizeCache == nil {
					serviceData = append(serviceData, serviceDataItem{
						Path: full, Size: "calculating...", SizeRaw: -1,
					})
					return
				}
				size, walked, hit := sizeCache.get(full)
				if !hit {
					serviceData = append(serviceData, serviceDataItem{
						Path: full, Size: "calculating...", SizeRaw: -1,
					})
					return
				}
				sizeStr := formatBytes(size)
				if time.Since(walked) > time.Hour {
					sizeStr += " (stale)"
				}
				serviceData = append(serviceData, serviceDataItem{
					Path: full, Size: sizeStr, SizeRaw: size,
				})
			}
			addRow(servicePath)
			children, err := os.ReadDir(servicePath)
			if err != nil {
				continue
			}
			for _, child := range children {
				if !child.IsDir() {
					continue
				}
				addRow(filepath.Join(servicePath, child.Name()))
			}
		}
	}

	writeJSON(w, 200, systemInfoResponse{
		Hostname:      hostname,
		OS:            osRelease,
		Kernel:        kernel,
		Model:         model,
		CPUCores:      cpuCores,
		MemTotal:      memTotal,
		MemFree:       memFree,
		Uptime:        uptime,
		Networks:      networks,
		Storage:       storage,
		DockerStorage: dockerStorage,
		ServiceData:   serviceData,
	})
}

// dirSizeCache stores cached directory sizes. Walks are expensive on
// blockchain-size directories; the agent populates this in a background
// goroutine and handleSystemInfo serves from cache to keep responses fast.
type dirSizeCache struct {
	mu     sync.RWMutex
	sizes  map[string]int64
	walked map[string]time.Time
}

func newDirSizeCache() *dirSizeCache {
	return &dirSizeCache{
		sizes:  make(map[string]int64),
		walked: make(map[string]time.Time),
	}
}

func (c *dirSizeCache) get(path string) (int64, time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	size, ok := c.sizes[path]
	if !ok {
		return 0, time.Time{}, false
	}
	return size, c.walked[path], true
}

func (c *dirSizeCache) set(path string, size int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sizes[path] = size
	c.walked[path] = time.Now()
}

func (c *dirSizeCache) forget(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sizes, path)
	delete(c.walked, path)
}

// dockerStorageCache memoizes the output of `docker system df`. The command
// takes several seconds on a pi with many containers + image layers; running
// it on every /system/info request is the dominant cost of the handler.
// TTL matches dirSizeCache's walk cadence so the whole Info panel is
// "approximately 5-min fresh." Manual destructive actions (prune,
// prune-buildcache) call invalidate() so the next fetch is live.
type dockerStorageCache struct {
	mu        sync.RWMutex
	value     []dockerStorageItem
	fetchedAt time.Time
	ttl       time.Duration
}

func newDockerStorageCache(ttl time.Duration) *dockerStorageCache {
	return &dockerStorageCache{ttl: ttl}
}

func (c *dockerStorageCache) get() ([]dockerStorageItem, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.fetchedAt.IsZero() || time.Since(c.fetchedAt) > c.ttl {
		return nil, false
	}
	return c.value, true
}

func (c *dockerStorageCache) set(v []dockerStorageItem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = v
	c.fetchedAt = time.Now()
}

func (c *dockerStorageCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fetchedAt = time.Time{}
}

// walkDockerStorageForever populates storageCache in the background. Same
// shape as walkDataDirsForever: short startup delay so the HTTP server is
// up, then refresh every 5 min. Manual destructive actions call set()
// directly after invalidate() to surface the post-prune state immediately.
func walkDockerStorageForever(c *dockerStorageCache) {
	time.Sleep(5 * time.Second)
	for {
		c.set(fetchDockerStorage())
		time.Sleep(5 * time.Minute)
	}
}

// fetchDockerStorage runs `docker system df --format {{json .}}` and parses
// the per-type rows. Returns nil on any error so the handler can gracefully
// degrade rather than 500.
//
// Generous 60 s timeout — on a pi with many build-cache items the scan can
// take 15+ seconds. This function is called from the background goroutine
// (walkDockerStorageForever) so it's not bounded by an HTTP request budget.
func fetchDockerStorage() []dockerStorageItem {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "system", "df", "--format", "{{json .}}")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	var items []dockerStorageItem
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var raw struct {
			Type        string `json:"Type"`
			TotalCount  string `json:"TotalCount"`
			Size        string `json:"Size"`
			Reclaimable string `json:"Reclaimable"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err == nil {
			count, _ := strconv.Atoi(raw.TotalCount)
			// Strip " (NN%)" trailer (docker appends this to Images/Containers/Volumes
			// rows but not Build Cache). parseBytes needs a plain "47.33GB" string.
			reclaimRaw := raw.Reclaimable
			if i := strings.Index(reclaimRaw, " ("); i > 0 {
				reclaimRaw = reclaimRaw[:i]
			}
			items = append(items, dockerStorageItem{
				Type:           raw.Type,
				Count:          count,
				TotalSize:      formatSize(raw.Size),
				Reclaimable:    formatSize(raw.Reclaimable),
				ReclaimableRaw: int64(parseBytes(reclaimRaw)),
			})
		}
	}
	return items
}

// walkDataDirsForever populates sizeCache by enumerating dataRoot/* AND
// dataRoot/*/*. Indexing BOTH levels lets templates declare data dirs at
// either granularity:
//
//   - bitcoind: /srv/truffels/data/bitcoin/blockchain (2 levels)
//   - electrs:  /srv/truffels/data/electrs/db          (2 levels)
//   - ckpool:   /srv/truffels/data/ckpool              (1 level — whole dir)
//   - truffels: /srv/truffels/data/truffels            (1 level — whole dir)
//
// dev.15 only indexed the 2-level leaves when children existed, so ckpool
// and truffels' template paths got cache misses and showed "—" in the UI.
// Cost of indexing the parent too: one extra du -s per service per 5 min
// — cheap.
//
// Runs forever in a goroutine; first walk happens after a short delay so
// the HTTP server is up first.
func walkDataDirsForever(c *dirSizeCache, root string) {
	time.Sleep(5 * time.Second)
	for {
		seen := make(map[string]bool)
		if entries, err := os.ReadDir(root); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				servicePath := filepath.Join(root, e.Name())
				// Always index the top-level service path so templates that
				// declare a 1-level path (ckpool, truffels) get a hit.
				seen[servicePath] = true
				c.set(servicePath, dirSizeBytes(servicePath))

				children, err := os.ReadDir(servicePath)
				if err != nil {
					continue
				}
				for _, child := range children {
					if !child.IsDir() {
						continue
					}
					full := filepath.Join(servicePath, child.Name())
					seen[full] = true
					c.set(full, dirSizeBytes(full))
				}
			}
		}
		// Forget paths that vanished between walks.
		c.mu.RLock()
		toDrop := make([]string, 0)
		for path := range c.sizes {
			if !seen[path] {
				toDrop = append(toDrop, path)
			}
		}
		c.mu.RUnlock()
		for _, path := range toDrop {
			c.forget(path)
		}
		time.Sleep(5 * time.Minute)
	}
}

// dirSizeBytes walks `path` and returns total size in bytes. Returns 0 on any
// error to keep the system-info endpoint robust against permission-denied
// nested files (e.g. inside container-managed dirs).
func dirSizeBytes(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// formatBytes renders bytes as a human-friendly size (matches Docker's df output).
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
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
		JournalDiskUsage:  formatSize(usage),
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

// allowedRepoDirs are the only directories git operations may touch. Exact
// string match only — no prefix matching, no path cleaning. A cleaned path
// would let "/repo/../etc" normalise into something that passes.
var allowedRepoDirs = map[string]bool{
	"/repo":                          true,
	"/srv/truffels/data/ckpoolstats": true,
}

func isAllowedRepoDir(dir string) bool {
	return allowedRepoDirs[dir]
}

type gitCheckoutRequest struct {
	RepoDir   string `json:"repo_dir"`
	Tag       string `json:"tag"`
	RefScheme string `json:"ref_scheme,omitempty"`
}

func handleGitCheckout(w http.ResponseWriter, r *http.Request) {
	var req gitCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	// Validate repo_dir is one of the allowed paths
	if !isAllowedRepoDir(req.RepoDir) {
		writeJSON(w, 403, map[string]string{"error": "repo_dir not allowed"})
		return
	}
	if req.Tag == "" {
		writeJSON(w, 400, map[string]string{"error": "tag required"})
		return
	}
	// ref_scheme selects the validator. Absent means tag — the self-update
	// path predates this field and must keep its stricter check.
	valid := isValidTag(req.Tag)
	if req.RefScheme == "commit" {
		valid = isValidCommitHash(req.Tag)
	}
	if !valid {
		writeJSON(w, 400, map[string]string{"error": "invalid ref format"})
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

// isValidCommitHash accepts abbreviated and full git object names: lowercase
// hex, 7 to 40 characters. Deliberately separate from isValidTag so loosening
// one cannot weaken the other.
func isValidCommitHash(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
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
sleep 3
docker compose -f %s down --timeout 60
docker compose -f %s up -d --build
`, composePath, composePath)

	scriptPath := "/tmp/truffels-self-update.sh"
	// Write script via nsenter into host filesystem
	writeCmd := exec.Command("nsenter", "-t", "1", "-m", "--",
		"sh", "-c", fmt.Sprintf("cat > %s << 'SCRIPT'\n%sSCRIPT\nchmod +x %s", scriptPath, script, scriptPath))
	if out, err := writeCmd.CombinedOutput(); err != nil {
		writeJSON(w, 500, map[string]string{"error": "write script failed: " + err.Error(), "output": string(out)})
		return
	}

	// Stop any previous transient unit (ignore errors — it may not exist)
	_ = exec.Command("nsenter", "-t", "1", "-m", "-p", "--",
		"systemctl", "stop", "truffels-self-update.service").Run()
	_ = exec.Command("nsenter", "-t", "1", "-m", "-p", "--",
		"systemctl", "reset-failed", "truffels-self-update.service").Run()

	// Execute as a systemd transient unit on the host via nsenter.
	// Using systemd-run creates an independent cgroup, so the script survives
	// when docker compose down kills all processes in the agent container's cgroup.
	execCmd := exec.Command("nsenter", "-t", "1", "-m", "-p", "--",
		"systemd-run", "--unit=truffels-self-update", "--collect",
		"/bin/sh", "-c", fmt.Sprintf("%s > /tmp/truffels-self-update.log 2>&1", scriptPath))
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
	OldTag    string   `json:"old_tag,omitempty"` // optional — if empty, matches any current tag
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
	if len(req.Images) == 0 || req.NewTag == "" {
		writeJSON(w, 400, map[string]string{"error": "images and new_tag are required"})
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
	matched := 0
	for _, img := range req.Images {
		// Match any current tag for this image (idempotent — works regardless of what tag is in the file)
		pattern := fmt.Sprintf(`(image:\s*)%s:[^\s@]+(@sha256:[a-f0-9]+)?`, regexp.QuoteMeta(img))
		re, err := regexp.Compile(pattern)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "compile regex for " + img + ": " + err.Error()})
			return
		}
		if re.MatchString(content) {
			matched++
		}
		replacement := fmt.Sprintf("${1}%s:%s", img, req.NewTag)
		content = re.ReplaceAllString(content, replacement)
	}

	// Rewrite VERSION build args — match any version value (idempotent)
	versionRe := regexp.MustCompile(`(VERSION:\s+)\S+`)
	content = versionRe.ReplaceAllString(content, "${1}"+req.NewTag)

	if matched == 0 {
		writeJSON(w, 400, map[string]string{"error": "no image tags matched in compose file"})
		return
	}

	// Already at target version — no write needed, return success
	if content == string(data) {
		writeJSON(w, 200, map[string]string{"status": "ok", "note": "already at target version"})
		return
	}

	if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
		writeJSON(w, 500, map[string]string{"error": "write compose file: " + err.Error()})
		return
	}

	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// handleComposeRead returns the content of a service's compose file.
func handleComposeRead(w http.ResponseWriter, r *http.Request) {
	var req serviceRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	composePath := composeDir(req.ServiceID) + "/docker-compose.yml"
	data, err := os.ReadFile(composePath)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "read compose file: " + err.Error()})
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"status":  "ok",
		"content": string(data),
	})
}

// handleComposeReconcile compares expected content with the compose file on disk,
// writes if different, and reports whether a change was made.
func handleComposeReconcile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServiceID       string `json:"service_id"`
		ExpectedContent string `json:"expected_content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if _, ok := allowedServices[req.ServiceID]; !ok {
		writeJSON(w, 403, map[string]string{"error": "service not allowed: " + req.ServiceID})
		return
	}
	if req.ExpectedContent == "" {
		writeJSON(w, 400, map[string]string{"error": "expected_content is required"})
		return
	}

	slog.Info("compose reconcile", "service", req.ServiceID)

	composePath := composeDir(req.ServiceID) + "/docker-compose.yml"
	data, err := os.ReadFile(composePath)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "read compose file: " + err.Error()})
		return
	}

	// Byte-for-byte comparison
	if bytes.Equal(data, []byte(req.ExpectedContent)) {
		writeJSON(w, 200, map[string]interface{}{"status": "ok", "changed": false})
		return
	}

	if err := os.WriteFile(composePath, []byte(req.ExpectedContent), 0644); err != nil {
		writeJSON(w, 500, map[string]string{"error": "write compose file: " + err.Error()})
		return
	}

	slog.Info("compose file reconciled", "service", req.ServiceID)
	writeJSON(w, 200, map[string]interface{}{"status": "ok", "changed": true})
}

// handleFileReconcile compares expected content with a file under the compose root,
// writes if different, and reports whether a change was made.
func handleFileReconcile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path            string `json:"path"`
		ExpectedContent string `json:"expected_content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if req.Path == "" || req.ExpectedContent == "" {
		writeJSON(w, 400, map[string]string{"error": "path and expected_content are required"})
		return
	}

	// Security: accept paths under either composeRoot or configRoot.
	// Relative paths keep the legacy behavior of joining with composeRoot;
	// absolute paths must validate against one of the two allowed roots.
	fullPath := req.Path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(composeRoot, filepath.Clean(req.Path))
	} else {
		fullPath = filepath.Clean(fullPath)
	}
	if _, err := validateUnderRoot(fullPath, composeRoot); err != nil {
		// Fall back to configRoot only if it's non-empty — an empty root would
		// match everything (HasPrefix(anything, "/") is true).
		if configRoot == "" {
			writeJSON(w, 403, map[string]string{"error": "path outside compose root: " + err.Error()})
			return
		}
		if _, err2 := validateUnderRoot(fullPath, configRoot); err2 != nil {
			writeJSON(w, 403, map[string]string{
				"error": "path outside allowed roots: " + err.Error() + " / " + err2.Error(),
			})
			return
		}
	}

	slog.Info("file reconcile", "path", fullPath)

	data, err := os.ReadFile(fullPath)
	if err != nil && !os.IsNotExist(err) {
		writeJSON(w, 500, map[string]string{"error": "read file: " + err.Error()})
		return
	}

	if bytes.Equal(data, []byte(req.ExpectedContent)) {
		writeJSON(w, 200, map[string]interface{}{"status": "ok", "changed": false})
		return
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		writeJSON(w, 500, map[string]string{"error": "create dir: " + err.Error()})
		return
	}

	if err := os.WriteFile(fullPath, []byte(req.ExpectedContent), 0644); err != nil {
		writeJSON(w, 500, map[string]string{"error": "write file: " + err.Error()})
		return
	}

	slog.Info("file reconciled", "path", fullPath)
	writeJSON(w, 200, map[string]interface{}{"status": "ok", "changed": true})
}

// validateUnderRoot returns the cleaned absolute path if it lies under `root`,
// is not a symlink redirecting outside `root`, and contains no relative-path
// escapes. Returns ("", error) otherwise. The path's parent must exist for
// EvalSymlinks to resolve cleanly; if the parent doesn't exist yet, the check
// walks upward until it finds an existing ancestor and validates that one.
func validateUnderRoot(p, root string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	cleaned := filepath.Clean(p)
	if !strings.HasPrefix(cleaned, root+"/") {
		return "", fmt.Errorf("path outside root")
	}
	// Walk up to the nearest existing ancestor and EvalSymlinks it. This catches
	// symlink redirects (e.g. /srv/truffels/data/x is a symlink to /etc).
	probe := cleaned
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe || parent == "/" {
			break
		}
		probe = parent
	}
	resolved, err := filepath.EvalSymlinks(probe)
	if err == nil && resolved != probe {
		// A symlink was traversed somewhere up the chain. Verify the target
		// still lies under root; reject if it escaped.
		if !strings.HasPrefix(resolved+"/", root+"/") && resolved != root {
			return "", fmt.Errorf("symlink redirects outside root")
		}
	}
	return cleaned, nil
}

// handleEnsureDir creates a directory under dataRoot with the requested
// ownership and mode. Idempotent. Used by the compose reconciler to guarantee
// bind-mount source paths exist with correct uid:gid before `docker compose up`.
func handleEnsureDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		UID  int    `json:"uid"`
		GID  int    `json:"gid"`
		Mode string `json:"mode"` // octal string, e.g. "0755"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if req.Mode == "" {
		req.Mode = "0755"
	}
	mode64, err := strconv.ParseUint(req.Mode, 8, 32)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid mode: " + err.Error()})
		return
	}
	mode := os.FileMode(uint32(mode64))

	cleaned, err := validateUnderRoot(req.Path, dataRoot)
	if err != nil {
		writeJSON(w, 403, map[string]string{"error": err.Error()})
		return
	}

	existed := true
	if _, err := os.Stat(cleaned); os.IsNotExist(err) {
		existed = false
	}

	if err := os.MkdirAll(cleaned, mode); err != nil {
		writeJSON(w, 500, map[string]string{"error": "mkdir: " + err.Error()})
		return
	}
	if err := os.Chown(cleaned, req.UID, req.GID); err != nil {
		writeJSON(w, 500, map[string]string{"error": "chown: " + err.Error()})
		return
	}
	if err := os.Chmod(cleaned, mode); err != nil {
		writeJSON(w, 500, map[string]string{"error": "chmod: " + err.Error()})
		return
	}

	slog.Info("ensure-dir", "path", cleaned, "uid", req.UID, "gid", req.GID, "created", !existed)
	writeJSON(w, 200, map[string]interface{}{"status": "ok", "created": !existed})
}

// clearDirBasenameAllowlist limits clear-dir to known-safe directory names.
// Adding to this set requires explicit review.
var clearDirBasenameAllowlist = map[string]bool{
	"cache": true,
	"logs":  true,
}

// handleClearDir empties a directory under dataRoot, then recreates it with
// the requested ownership/mode. Double-locked: basename must be in allowlist
// AND path must be exactly two levels under dataRoot (i.e. dataRoot/<service>/<basename>).
// This rejects paths like /srv/truffels/data/bitcoin/blockchain/cache.
func handleClearDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		UID  int    `json:"uid"`
		GID  int    `json:"gid"`
		Mode string `json:"mode"` // octal string, e.g. "0755"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	if req.Mode == "" {
		req.Mode = "0755"
	}
	mode64, err := strconv.ParseUint(req.Mode, 8, 32)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid mode: " + err.Error()})
		return
	}
	mode := os.FileMode(uint32(mode64))

	cleaned, err := validateUnderRoot(req.Path, dataRoot)
	if err != nil {
		writeJSON(w, 403, map[string]string{"error": err.Error()})
		return
	}

	// Basename allowlist
	base := filepath.Base(cleaned)
	if !clearDirBasenameAllowlist[base] {
		writeJSON(w, 403, map[string]string{"error": "basename not in clear allowlist: " + base})
		return
	}

	// Exact depth check: path must be dataRoot/<service>/<basename> — exactly two levels deep.
	// This rejects e.g. /srv/truffels/data/bitcoin/blockchain/cache.
	rel := strings.TrimPrefix(cleaned, dataRoot+"/")
	parts := strings.Split(rel, "/")
	if len(parts) != 2 {
		writeJSON(w, 403, map[string]string{"error": "clear-dir path must be exactly dataRoot/<service>/<dir>"})
		return
	}

	slog.Info("clear-dir", "path", cleaned, "uid", req.UID, "gid", req.GID)

	if err := os.RemoveAll(cleaned); err != nil {
		writeJSON(w, 500, map[string]string{"error": "remove: " + err.Error()})
		return
	}
	if err := os.MkdirAll(cleaned, mode); err != nil {
		writeJSON(w, 500, map[string]string{"error": "mkdir: " + err.Error()})
		return
	}
	if err := os.Chown(cleaned, req.UID, req.GID); err != nil {
		writeJSON(w, 500, map[string]string{"error": "chown: " + err.Error()})
		return
	}
	if err := os.Chmod(cleaned, mode); err != nil {
		writeJSON(w, 500, map[string]string{"error": "chmod: " + err.Error()})
		return
	}

	// Update the dir-size cache so the System Info "Service Data" panel
	// reflects the clear immediately, instead of showing the pre-clear size
	// until the next 5-min walk. The panel header says "Clearing a directory
	// updates immediately" — this makes that contract true.
	if sizeCache != nil {
		sizeCache.set(cleaned, 0)
	}

	writeJSON(w, 200, map[string]interface{}{"status": "ok"})
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
