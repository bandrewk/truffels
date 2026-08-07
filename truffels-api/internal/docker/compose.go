package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
			Timeout: 6 * time.Minute,
		},
	}
}

type agentServiceReq struct {
	ServiceID string `json:"service_id"`
}

type agentLogsReq struct {
	ServiceID string `json:"service_id"`
	Tail      int    `json:"tail"`
	Since     string `json:"since,omitempty"`
	Container string `json:"container,omitempty"`
}

type agentResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Logs   string `json:"logs"`
	Output string `json:"output"`
}

type ImageInfo struct {
	Image  string            `json:"image"`
	Digest string            `json:"digest"`
	Tags   []string          `json:"tags"`
	Labels map[string]string `json:"labels,omitempty"`
}

func (c *ComposeClient) Up(serviceID string) error {
	return c.composeAction("/v1/compose/up", serviceID)
}

func (c *ComposeClient) Down(serviceID string) error {
	return c.composeAction("/v1/compose/down", serviceID)
}

func (c *ComposeClient) Stop(serviceID string) error {
	return c.composeAction("/v1/compose/stop", serviceID)
}

func (c *ComposeClient) Restart(serviceID string) error {
	return c.composeAction("/v1/compose/restart", serviceID)
}

func (c *ComposeClient) Logs(serviceID string, tail int, since, container string) (string, error) {
	body, _ := json.Marshal(agentLogsReq{ServiceID: serviceID, Tail: tail, Since: since, Container: container})
	resp, err := c.httpClient.Post(c.agentURL+"/v1/compose/logs", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("agent logs: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return ar.Logs, fmt.Errorf("agent logs: %s", ar.Error)
	}
	return ar.Logs, nil
}

// Pull pulls a Docker image via the agent. Returns the docker pull output.
func (c *ComposeClient) Pull(image string) (string, error) {
	body, _ := json.Marshal(map[string]string{"image": image})
	slog.Info("agent pull", "image", image)

	longClient := &http.Client{Timeout: 20 * time.Minute}
	resp, err := longClient.Post(c.agentURL+"/v1/image/pull", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("agent pull: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		var ar agentResponse
		_ = json.NewDecoder(resp.Body).Decode(&ar)
		return nil, fmt.Errorf("agent image inspect: %s", ar.Error)
	}

	var info ImageInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("agent image inspect decode: %w", err)
	}
	return &info, nil
}

// ImageInspectByName returns image info for an image reference, without going
// through a container. Needed for custom-built services: their container may be
// absent (never started, or removed by a failed update), and the container-based
// lookup then reports nothing at all.
func (c *ComposeClient) ImageInspectByName(image string) (*ImageInfo, error) {
	body, _ := json.Marshal(map[string]string{"image": image})

	resp, err := c.httpClient.Post(c.agentURL+"/v1/image/inspect-by-name", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("agent image inspect by name: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		var ar agentResponse
		_ = json.NewDecoder(resp.Body).Decode(&ar)
		return nil, fmt.Errorf("agent image inspect by name: %s", ar.Error)
	}

	var info ImageInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("agent image inspect by name decode: %w", err)
	}
	return &info, nil
}

// ImageTag points target at the image currently behind source. Used to stage
// and to restore the rollback generation of a custom-built service.
func (c *ComposeClient) ImageTag(source, target string) error {
	body, _ := json.Marshal(map[string]string{"source": source, "target": target})
	slog.Info("agent image tag", "source", source, "target", target)

	resp, err := c.httpClient.Post(c.agentURL+"/v1/image/tag", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent image tag: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent image tag: %s", ar.Error)
	}
	return nil
}

// Build runs docker compose build for a service via the agent.
func (c *ComposeClient) Build(serviceID string) error {
	body, _ := json.Marshal(agentServiceReq{ServiceID: serviceID})
	slog.Info("agent build", "service", serviceID)

	longClient := &http.Client{Timeout: 20 * time.Minute}
	resp, err := longClient.Post(c.agentURL+"/v1/compose/build", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent build: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
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
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent system %s: %s", action, ar.Error)
	}
	return nil
}

// BootEntry represents a journal boot entry.
type BootEntry struct {
	Index int    `json:"index"`
	ID    string `json:"id"`
	First string `json:"first"`
	Last  string `json:"last"`
}

// SystemTuningInfo represents host tuning parameters.
type SystemTuningInfo struct {
	PersistentJournal bool        `json:"persistent_journal"`
	Swappiness        int         `json:"swappiness"`
	JournalDiskUsage  string      `json:"journal_disk_usage"`
	Boots             []BootEntry `json:"boots"`
}

// NetworkIfInfo represents a network interface.
type NetworkIfInfo struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	MAC  string `json:"mac"`
}

// StorageInfo represents a filesystem mount.
type StorageInfo struct {
	Device string `json:"device"`
	Mount  string `json:"mount"`
	FSType string `json:"fstype"`
	Size   string `json:"size"`
	Used   string `json:"used"`
	Free   string `json:"free"`
	UsePct string `json:"use_pct"`
}

// DockerStorageItem represents a row from docker system df.
type DockerStorageItem struct {
	Type           string `json:"type"`
	Count          int    `json:"count"`
	TotalSize      string `json:"total_size"`
	Reclaimable    string `json:"reclaimable"`
	ReclaimableRaw int64  `json:"reclaimable_raw"` // bytes — for client-side threshold gates
}

// ServiceDataItem represents a host data directory size — populated for paths
// under /srv/truffels/data/* by the agent.
type ServiceDataItem struct {
	Path    string `json:"path"`
	Size    string `json:"size"`
	SizeRaw int64  `json:"size_raw"`
}

// SystemInfo represents host system information.
type SystemInfo struct {
	Hostname      string              `json:"hostname"`
	OS            string              `json:"os"`
	Kernel        string              `json:"kernel"`
	Model         string              `json:"model"`
	CPUCores      int                 `json:"cpu_cores"`
	MemTotal      string              `json:"mem_total"`
	MemFree       string              `json:"mem_free"`
	Uptime        string              `json:"uptime"`
	Networks      []NetworkIfInfo     `json:"networks"`
	Storage       []StorageInfo       `json:"storage"`
	DockerStorage []DockerStorageItem `json:"docker_storage,omitempty"`
	ServiceData   []ServiceDataItem   `json:"service_data,omitempty"`
}

// SystemInfoGet fetches host system info via the agent.
func (c *ComposeClient) SystemInfoGet() (*SystemInfo, error) {
	resp, err := c.httpClient.Get(c.agentURL + "/v1/system/info")
	if err != nil {
		return nil, fmt.Errorf("agent system info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var info SystemInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("agent system info decode: %w", err)
	}
	return &info, nil
}

// HostDirSize asks the agent for the cached size of a data-dir path.
// Returns (-1, false, nil) if the agent has not yet walked the path.
func (c *ComposeClient) HostDirSize(path string) (int64, bool, error) {
	resp, err := c.httpClient.Get(c.agentURL + "/v1/host/dir-size?path=" + url.QueryEscape(path))
	if err != nil {
		return 0, false, fmt.Errorf("agent dir-size: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		SizeBytes int64 `json:"size_bytes"`
		Fresh     bool  `json:"fresh"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, false, fmt.Errorf("agent dir-size decode: %w", err)
	}
	return out.SizeBytes, out.Fresh, nil
}

// SystemJournal fetches journalctl output via the agent.
func (c *ComposeClient) SystemJournal(lines int, priority, unit, since string, boot int) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"lines": lines, "priority": priority, "unit": unit, "since": since, "boot": boot,
	})
	resp, err := c.httpClient.Post(c.agentURL+"/v1/system/journal", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("agent journal: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return ar.Logs, fmt.Errorf("agent journal: %s", ar.Error)
	}
	return ar.Logs, nil
}

// SystemTuningGet reads current host tuning values via the agent.
func (c *ComposeClient) SystemTuningGet() (*SystemTuningInfo, error) {
	resp, err := c.httpClient.Get(c.agentURL + "/v1/system/tuning")
	if err != nil {
		return nil, fmt.Errorf("agent tuning get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		var ar agentResponse
		_ = json.NewDecoder(resp.Body).Decode(&ar)
		return nil, fmt.Errorf("agent tuning get: %s", ar.Error)
	}

	var info SystemTuningInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("agent tuning decode: %w", err)
	}
	return &info, nil
}

// SystemTuningSet applies a tuning change via the agent.
func (c *ComposeClient) SystemTuningSet(action, value string) error {
	body, _ := json.Marshal(map[string]string{"action": action, "value": value})
	resp, err := c.httpClient.Post(c.agentURL+"/v1/system/tuning", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent tuning set: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent tuning set: %s", ar.Error)
	}
	return nil
}

// GitCheckout tells the agent to fetch and checkout a specific ref.
// refScheme is "tag" or "commit"; empty means tag.
func (c *ComposeClient) GitCheckout(repoDir, ref, refScheme string) error {
	body, _ := json.Marshal(map[string]string{
		"repo_dir": repoDir, "tag": ref, "ref_scheme": refScheme,
	})
	slog.Info("agent git checkout", "repo", repoDir, "ref", ref, "scheme", refScheme)

	resp, err := c.httpClient.Post(c.agentURL+"/v1/git/checkout", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent git checkout: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent git checkout: %s", ar.Error)
	}
	return nil
}

// BuildWithArgs runs docker compose build with extra build args via the agent.
// Optional services parameter specifies which compose services to build (empty = all).
func (c *ComposeClient) BuildWithArgs(serviceID string, buildArgs map[string]string, services ...string) error {
	type buildReq struct {
		ServiceID string            `json:"service_id"`
		BuildArgs map[string]string `json:"build_args,omitempty"`
		Services  []string          `json:"services,omitempty"`
	}
	body, _ := json.Marshal(buildReq{ServiceID: serviceID, BuildArgs: buildArgs, Services: services})
	slog.Info("agent build with args", "service", serviceID, "args", buildArgs, "services", services)

	longClient := &http.Client{Timeout: 20 * time.Minute}
	resp, err := longClient.Post(c.agentURL+"/v1/compose/build", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent build: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		msg := ar.Error
		// Include last ~500 chars of build output for debugging
		if ar.Output != "" {
			out := ar.Output
			if len(out) > 500 {
				out = "..." + out[len(out)-500:]
			}
			msg += "\n" + out
		}
		return fmt.Errorf("agent build: %s", msg)
	}
	return nil
}

// ComposeUpDetached triggers a detached compose up via nsenter on the host.
// This is used for self-updates where the agent container will be replaced.
// Returns immediately with 202 Accepted.
func (c *ComposeClient) ComposeUpDetached(serviceID string) error {
	body, _ := json.Marshal(map[string]string{"service_id": serviceID})
	slog.Info("agent compose up detached", "service", serviceID)

	resp, err := c.httpClient.Post(c.agentURL+"/v1/compose/up-detached", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent compose up detached: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		return fmt.Errorf("agent compose up detached: %s", ar.Error)
	}
	return nil
}

// RewriteTags rewrites image tags in a compose file via the agent.
// oldTag is optional — if empty, the agent matches any current tag (idempotent).
func (c *ComposeClient) RewriteTags(serviceID string, images []string, oldTag, newTag string) error {
	req := map[string]interface{}{
		"service_id": serviceID,
		"images":     images,
		"new_tag":    newTag,
	}
	if oldTag != "" {
		req["old_tag"] = oldTag
	}
	body, _ := json.Marshal(req)
	slog.Info("agent rewrite tags", "service", serviceID, "old", oldTag, "new", newTag)

	resp, err := c.httpClient.Post(c.agentURL+"/v1/compose/rewrite-tags", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent rewrite tags: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent rewrite tags: %s", ar.Error)
	}
	return nil
}

// RemoveImage removes a Docker image via the agent (best-effort).
func (c *ComposeClient) RemoveImage(image string) error {
	body, _ := json.Marshal(map[string]string{"image": image})
	slog.Info("agent remove image", "image", image)

	resp, err := c.httpClient.Post(c.agentURL+"/v1/image/remove", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent remove image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent remove image: %s", ar.Error)
	}
	return nil
}

// FsEnsureDir creates a directory under the agent's dataRoot with the requested
// ownership and mode. Idempotent. Used by the compose reconciler to guarantee
// bind-mount source paths exist before bringing a service up.
func (c *ComposeClient) FsEnsureDir(path string, uid, gid int, mode string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"path": path, "uid": uid, "gid": gid, "mode": mode,
	})
	resp, err := c.httpClient.Post(c.agentURL+"/v1/fs/ensure-dir", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent ensure-dir: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent ensure-dir: %s", ar.Error)
	}
	return nil
}

// FsClearDir empties a directory under the agent's dataRoot and recreates it
// with the requested ownership/mode. The agent enforces a basename+depth
// allowlist; callers must ensure the path is one we want to expose.
func (c *ComposeClient) FsClearDir(path string, uid, gid int, mode string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"path": path, "uid": uid, "gid": gid, "mode": mode,
	})
	resp, err := c.httpClient.Post(c.agentURL+"/v1/fs/clear-dir", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent clear-dir: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent clear-dir: %s", ar.Error)
	}
	return nil
}

// FileReconcile writes `content` to `path` via the agent's POST /v1/file/reconcile
// endpoint. Used by the reconciler for config files (Caddyfile) that live outside
// composeRoot. Returns (changed, error).
func (c *ComposeClient) FileReconcile(path, content string) (bool, error) {
	body, _ := json.Marshal(map[string]string{
		"path":             path,
		"expected_content": content,
	})
	resp, err := c.httpClient.Post(c.agentURL+"/v1/file/reconcile", "application/json", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("agent file reconcile: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var result struct {
		Status  string `json:"status"`
		Error   string `json:"error"`
		Changed bool   `json:"changed"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("agent file reconcile: %s", result.Error)
	}
	return result.Changed, nil
}

// DockerPrune runs a full docker cleanup via the agent (builder + image + system prune).
func (c *ComposeClient) DockerPrune() (string, error) {
	longClient := &http.Client{Timeout: 6 * time.Minute}
	resp, err := longClient.Post(c.agentURL+"/v1/docker/prune", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", fmt.Errorf("agent docker prune: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Status    string `json:"status"`
		Error     string `json:"error"`
		Reclaimed string `json:"reclaimed"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("agent docker prune: %s", result.Error)
	}
	return result.Reclaimed, nil
}

// DockerPruneBuildCache runs only docker builder prune via the agent.
func (c *ComposeClient) DockerPruneBuildCache() (string, error) {
	longClient := &http.Client{Timeout: 6 * time.Minute}
	resp, err := longClient.Post(c.agentURL+"/v1/docker/prune-buildcache", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", fmt.Errorf("agent docker prune buildcache: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Status    string `json:"status"`
		Error     string `json:"error"`
		Reclaimed string `json:"reclaimed"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("agent docker prune buildcache: %s", result.Error)
	}
	return result.Reclaimed, nil
}

// ComposeRead returns the content of a service's compose file via the agent.
func (c *ComposeClient) ComposeRead(serviceID string) (string, error) {
	body, _ := json.Marshal(agentServiceReq{ServiceID: serviceID})

	resp, err := c.httpClient.Post(c.agentURL+"/v1/compose/read", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("agent compose read: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Status  string `json:"status"`
		Error   string `json:"error"`
		Content string `json:"content"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("agent compose read: %s", result.Error)
	}
	return result.Content, nil
}

// ComposeReconcile compares expected content with the compose file on disk via the agent.
// Returns true if the file was changed.
func (c *ComposeClient) ComposeReconcile(serviceID, expectedContent string) (bool, error) {
	body, _ := json.Marshal(map[string]string{
		"service_id":       serviceID,
		"expected_content": expectedContent,
	})

	resp, err := c.httpClient.Post(c.agentURL+"/v1/compose/reconcile", "application/json", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("agent compose reconcile: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Status  string `json:"status"`
		Error   string `json:"error"`
		Changed bool   `json:"changed"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("agent compose reconcile: %s", result.Error)
	}
	return result.Changed, nil
}

// ReconcileFile compares expected content with a file under the compose root via the agent.
// The path is relative to /srv/truffels/compose/ (e.g. "ckstats/Dockerfile").
// Returns true if the file was changed.
func (c *ComposeClient) ReconcileFile(relativePath, content string) (bool, error) {
	body, _ := json.Marshal(map[string]string{
		"path":             relativePath,
		"expected_content": content,
	})

	resp, err := c.httpClient.Post(c.agentURL+"/v1/file/reconcile", "application/json", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("agent file reconcile: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Status  string `json:"status"`
		Error   string `json:"error"`
		Changed bool   `json:"changed"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("agent file reconcile: %s", result.Error)
	}
	return result.Changed, nil
}

func (c *ComposeClient) composeAction(path, serviceID string) error {
	body, _ := json.Marshal(agentServiceReq{ServiceID: serviceID})
	slog.Info("agent request", "path", path, "service", serviceID)

	resp, err := c.httpClient.Post(c.agentURL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent %s: %s", path, ar.Error)
	}
	return nil
}
