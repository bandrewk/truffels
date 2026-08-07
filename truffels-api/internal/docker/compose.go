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

const (
	// defaultTimeout bounds an ordinary agent call.
	defaultTimeout = 6 * time.Minute
	// buildTimeout covers image pulls and builds, which routinely outrun the
	// default budget on a Pi.
	buildTimeout = 20 * time.Minute
)

type ComposeClient struct {
	agentURL   string
	httpClient *http.Client
}

func NewComposeClient(agentURL string) *ComposeClient {
	return &ComposeClient{
		agentURL: agentURL,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
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

// agentResult is the agent's other reply shape, used by the endpoints that
// answer with a concrete payload field instead of the logs/output envelope.
// These deliberately do not go through agentError: they surface only the error
// line, which is what their callers have always shown.
type agentResult struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	Changed   bool   `json:"changed"`
	Reclaimed string `json:"reclaimed"`
	Content   string `json:"content"`
}

// agentError builds an error from a failed agent response. ar.Error alone is
// often just an exit status ("git checkout failed: exit status 1" / "build
// failed: exit status 1"); the actual diagnosis — which untracked files were
// in the way, which ref failed to resolve, the failing build step — is in
// ar.Output. Dropping it made failures indistinguishable in the UI and the
// cause had to be fetched off the device by hand. Output is tail-truncated to
// the last 500 chars: the interesting lines are printed last, by git and by
// docker build alike.
func agentError(op string, ar agentResponse) error {
	msg := ar.Error
	if ar.Output != "" {
		out := ar.Output
		if len(out) > 500 {
			out = "..." + out[len(out)-500:]
		}
		msg += "\n" + out
	}
	return fmt.Errorf("%s: %s", op, msg)
}

// agentReq describes one call to the agent: where it goes, what it carries, and
// the three knobs that a handful of endpoints need.
type agentReq struct {
	// path is appended to agentURL and may carry a query string.
	path string
	// payload is marshalled as the JSON request body. A nil payload makes the
	// call a GET.
	payload any
	// extraOK is a second status code to accept besides 200. Only the detached
	// compose up needs it: it answers 202 before the work has finished.
	extraOK int
	// timeout replaces the shared client's budget for this one call.
	timeout time.Duration
	// decodeOp overrides the operation name used when an accepted response
	// fails to parse. Only the tuning endpoint needs it: its decode error has
	// always read "agent tuning decode", not "agent tuning get decode".
	decodeOp string
}

func (r agentReq) accepts(status int) bool {
	return status == 200 || (r.extraOK != 0 && status == r.extraOK)
}

func (r agentReq) decodeOpFor(op string) string {
	if r.decodeOp != "" {
		return r.decodeOp
	}
	return op + " decode"
}

// send performs the request described by r. Transport failures are wrapped with
// op and %w, so callers can still reach the underlying *url.Error. On success
// the caller owns the response body.
func (c *ComposeClient) send(op string, r agentReq) (*http.Response, error) {
	client := c.httpClient
	if r.timeout > 0 {
		client = &http.Client{Timeout: r.timeout}
	}

	var (
		resp *http.Response
		err  error
	)
	if r.payload == nil {
		resp, err = client.Get(c.agentURL + r.path)
	} else {
		body, _ := json.Marshal(r.payload)
		resp, err = client.Post(c.agentURL+r.path, "application/json", bytes.NewReader(body))
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return resp, nil
}

// call sends r and returns the decoded agent envelope. A rejected status yields
// an agentError, which keeps the agent's output in the message. The envelope is
// returned in that case too — Logs and SystemJournal hand the partial text back
// to their callers alongside the error. A body that will not parse is ignored:
// only the status code decides.
func (c *ComposeClient) call(op string, r agentReq) (agentResponse, error) {
	resp, err := c.send(op, r)
	if err != nil {
		return agentResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var ar agentResponse
	_ = json.NewDecoder(resp.Body).Decode(&ar)
	if !r.accepts(resp.StatusCode) {
		return ar, agentError(op, ar)
	}
	return ar, nil
}

// callInto sends r and decodes an accepted response into out. A rejected status
// is reported through agentError, a body that will not parse with the decode
// operation name.
func (c *ComposeClient) callInto(op string, r agentReq, out any) error {
	resp, err := c.send(op, r)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if !r.accepts(resp.StatusCode) {
		var ar agentResponse
		_ = json.NewDecoder(resp.Body).Decode(&ar)
		return agentError(op, ar)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: %w", r.decodeOpFor(op), err)
	}
	return nil
}

// callDecode sends r and decodes the body into out without consulting the
// status code. The two host-info endpoints have always behaved this way: an
// agent that answers with a parseable body is treated as a success even when it
// reports a failure status.
func (c *ComposeClient) callDecode(op string, r agentReq, out any) error {
	resp, err := c.send(op, r)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s: %w", r.decodeOpFor(op), err)
	}
	return nil
}

// callResult sends r and returns the agent's payload reply. Failures report only
// res.Error — these endpoints do not carry an output field to append.
func (c *ComposeClient) callResult(op string, r agentReq) (agentResult, error) {
	resp, err := c.send(op, r)
	if err != nil {
		return agentResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var res agentResult
	_ = json.NewDecoder(resp.Body).Decode(&res)
	if !r.accepts(resp.StatusCode) {
		return agentResult{}, fmt.Errorf("%s: %s", op, res.Error)
	}
	return res, nil
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
	ar, err := c.call("agent logs", agentReq{
		path:    "/v1/compose/logs",
		payload: agentLogsReq{ServiceID: serviceID, Tail: tail, Since: since, Container: container},
	})
	// ar.Logs is returned on both paths: a failed call still hands back
	// whatever the agent managed to collect before giving up.
	return ar.Logs, err
}

// Pull pulls a Docker image via the agent. Returns the docker pull output.
func (c *ComposeClient) Pull(image string) (string, error) {
	slog.Info("agent pull", "image", image)

	// agentError keeps the tail of ar.Output. A failed pull says why in it —
	// manifest unknown, no space left, auth required — and that text is what
	// reaches the update log and the alert.
	ar, err := c.call("agent pull", agentReq{
		path:    "/v1/image/pull",
		payload: map[string]string{"image": image},
		timeout: buildTimeout,
	})
	if err != nil {
		return "", err
	}
	return ar.Output, nil
}

// ImageInspect returns image info for a running container via the agent.
func (c *ComposeClient) ImageInspect(container string) (*ImageInfo, error) {
	var info ImageInfo
	if err := c.callInto("agent image inspect", agentReq{
		path:    "/v1/image/inspect",
		payload: map[string]string{"container": container},
	}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ImageInspectByName returns image info for an image reference, without going
// through a container. Needed for custom-built services: their container may be
// absent (never started, or removed by a failed update), and the container-based
// lookup then reports nothing at all.
func (c *ComposeClient) ImageInspectByName(image string) (*ImageInfo, error) {
	var info ImageInfo
	if err := c.callInto("agent image inspect by name", agentReq{
		path:    "/v1/image/inspect-by-name",
		payload: map[string]string{"image": image},
	}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// ImageTag points target at the image currently behind source. Used to stage
// and to restore the rollback generation of a custom-built service.
func (c *ComposeClient) ImageTag(source, target string) error {
	slog.Info("agent image tag", "source", source, "target", target)

	_, err := c.call("agent image tag", agentReq{
		path:    "/v1/image/tag",
		payload: map[string]string{"source": source, "target": target},
	})
	return err
}

// Build runs docker compose build for a service via the agent.
func (c *ComposeClient) Build(serviceID string) error {
	slog.Info("agent build", "service", serviceID)

	_, err := c.call("agent build", agentReq{
		path:    "/v1/compose/build",
		payload: agentServiceReq{ServiceID: serviceID},
		timeout: buildTimeout,
	})
	return err
}

// SystemAction sends a shutdown or restart command to the agent.
func (c *ComposeClient) SystemAction(action string) error {
	_, err := c.call("agent system "+action, agentReq{
		path:    "/v1/system/" + action,
		payload: map[string]string{"action": action},
	})
	return err
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
	var info SystemInfo
	if err := c.callDecode("agent system info", agentReq{path: "/v1/system/info"}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// HostDirSize asks the agent for the cached size of a data-dir path.
// Returns (-1, false, nil) if the agent has not yet walked the path.
func (c *ComposeClient) HostDirSize(path string) (int64, bool, error) {
	var out struct {
		SizeBytes int64 `json:"size_bytes"`
		Fresh     bool  `json:"fresh"`
	}
	if err := c.callDecode("agent dir-size", agentReq{
		path: "/v1/host/dir-size?path=" + url.QueryEscape(path),
	}, &out); err != nil {
		return 0, false, err
	}
	return out.SizeBytes, out.Fresh, nil
}

// SystemJournal fetches journalctl output via the agent.
func (c *ComposeClient) SystemJournal(lines int, priority, unit, since string, boot int) (string, error) {
	ar, err := c.call("agent journal", agentReq{
		path: "/v1/system/journal",
		payload: map[string]any{
			"lines": lines, "priority": priority, "unit": unit, "since": since, "boot": boot,
		},
	})
	// As with Logs, the partial text goes back to the caller either way.
	return ar.Logs, err
}

// SystemTuningGet reads current host tuning values via the agent.
func (c *ComposeClient) SystemTuningGet() (*SystemTuningInfo, error) {
	var info SystemTuningInfo
	if err := c.callInto("agent tuning get", agentReq{
		path:     "/v1/system/tuning",
		decodeOp: "agent tuning decode",
	}, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// SystemTuningSet applies a tuning change via the agent.
func (c *ComposeClient) SystemTuningSet(action, value string) error {
	_, err := c.call("agent tuning set", agentReq{
		path:    "/v1/system/tuning",
		payload: map[string]string{"action": action, "value": value},
	})
	return err
}

// GitCheckout tells the agent to fetch and checkout a specific ref.
// refScheme is "tag" or "commit"; empty means tag.
func (c *ComposeClient) GitCheckout(repoDir, ref, refScheme string) error {
	slog.Info("agent git checkout", "repo", repoDir, "ref", ref, "scheme", refScheme)

	_, err := c.call("agent git checkout", agentReq{
		path: "/v1/git/checkout",
		payload: map[string]string{
			"repo_dir": repoDir, "tag": ref, "ref_scheme": refScheme,
		},
	})
	return err
}

// BuildWithArgs runs docker compose build with extra build args via the agent.
// Optional services parameter specifies which compose services to build (empty = all).
func (c *ComposeClient) BuildWithArgs(serviceID string, buildArgs map[string]string, services ...string) error {
	type buildReq struct {
		ServiceID string            `json:"service_id"`
		BuildArgs map[string]string `json:"build_args,omitempty"`
		Services  []string          `json:"services,omitempty"`
	}
	slog.Info("agent build with args", "service", serviceID, "args", buildArgs, "services", services)

	// Shares Build's operation name, and therefore its error prefix.
	_, err := c.call("agent build", agentReq{
		path:    "/v1/compose/build",
		payload: buildReq{ServiceID: serviceID, BuildArgs: buildArgs, Services: services},
		timeout: buildTimeout,
	})
	return err
}

// ComposeUpDetached triggers a detached compose up via nsenter on the host.
// This is used for self-updates where the agent container will be replaced.
// Returns immediately with 202 Accepted.
func (c *ComposeClient) ComposeUpDetached(serviceID string) error {
	slog.Info("agent compose up detached", "service", serviceID)

	_, err := c.call("agent compose up detached", agentReq{
		path:    "/v1/compose/up-detached",
		payload: map[string]string{"service_id": serviceID},
		extraOK: 202,
	})
	return err
}

// RewriteTags rewrites image tags in a compose file via the agent.
// oldTag is optional — if empty, the agent matches any current tag (idempotent).
func (c *ComposeClient) RewriteTags(serviceID string, images []string, oldTag, newTag string) error {
	req := map[string]any{
		"service_id": serviceID,
		"images":     images,
		"new_tag":    newTag,
	}
	if oldTag != "" {
		req["old_tag"] = oldTag
	}
	slog.Info("agent rewrite tags", "service", serviceID, "old", oldTag, "new", newTag)

	_, err := c.call("agent rewrite tags", agentReq{
		path:    "/v1/compose/rewrite-tags",
		payload: req,
	})
	return err
}

// RemoveImage removes a Docker image via the agent (best-effort).
func (c *ComposeClient) RemoveImage(image string) error {
	slog.Info("agent remove image", "image", image)

	_, err := c.call("agent remove image", agentReq{
		path:    "/v1/image/remove",
		payload: map[string]string{"image": image},
	})
	return err
}

// FsEnsureDir creates a directory under the agent's dataRoot with the requested
// ownership and mode. Idempotent. Used by the compose reconciler to guarantee
// bind-mount source paths exist before bringing a service up.
func (c *ComposeClient) FsEnsureDir(path string, uid, gid int, mode string) error {
	_, err := c.call("agent ensure-dir", agentReq{
		path: "/v1/fs/ensure-dir",
		payload: map[string]any{
			"path": path, "uid": uid, "gid": gid, "mode": mode,
		},
	})
	return err
}

// FsClearDir empties a directory under the agent's dataRoot and recreates it
// with the requested ownership/mode. The agent enforces a basename+depth
// allowlist; callers must ensure the path is one we want to expose.
func (c *ComposeClient) FsClearDir(path string, uid, gid int, mode string) error {
	_, err := c.call("agent clear-dir", agentReq{
		path: "/v1/fs/clear-dir",
		payload: map[string]any{
			"path": path, "uid": uid, "gid": gid, "mode": mode,
		},
	})
	return err
}

// FileReconcile writes `content` to `path` via the agent's POST /v1/file/reconcile
// endpoint. Used by the reconciler for config files (Caddyfile) that live outside
// composeRoot. Returns (changed, error).
func (c *ComposeClient) FileReconcile(path, content string) (bool, error) {
	res, err := c.callResult("agent file reconcile", agentReq{
		path: "/v1/file/reconcile",
		payload: map[string]string{
			"path":             path,
			"expected_content": content,
		},
	})
	return res.Changed, err
}

// ReconcileFile compares expected content with a file under the compose root via
// the agent. The path is relative to /srv/truffels/compose/ (e.g.
// "ckstats/Dockerfile"). Returns true if the file was changed.
//
// Same endpoint, payload and error text as FileReconcile, which takes an
// absolute path outside composeRoot; the two differ only in what the caller is
// expected to pass.
func (c *ComposeClient) ReconcileFile(relativePath, content string) (bool, error) {
	return c.FileReconcile(relativePath, content)
}

// DockerPrune runs a full docker cleanup via the agent (builder + image + system prune).
func (c *ComposeClient) DockerPrune() (string, error) {
	res, err := c.callResult("agent docker prune", agentReq{
		path:    "/v1/docker/prune",
		payload: map[string]any{},
	})
	return res.Reclaimed, err
}

// DockerPruneBuildCache runs only docker builder prune via the agent.
func (c *ComposeClient) DockerPruneBuildCache() (string, error) {
	res, err := c.callResult("agent docker prune buildcache", agentReq{
		path:    "/v1/docker/prune-buildcache",
		payload: map[string]any{},
	})
	return res.Reclaimed, err
}

// ComposeRead returns the content of a service's compose file via the agent.
func (c *ComposeClient) ComposeRead(serviceID string) (string, error) {
	res, err := c.callResult("agent compose read", agentReq{
		path:    "/v1/compose/read",
		payload: agentServiceReq{ServiceID: serviceID},
	})
	return res.Content, err
}

// ComposeReconcile compares expected content with the compose file on disk via the agent.
// Returns true if the file was changed.
func (c *ComposeClient) ComposeReconcile(serviceID, expectedContent string) (bool, error) {
	res, err := c.callResult("agent compose reconcile", agentReq{
		path: "/v1/compose/reconcile",
		payload: map[string]string{
			"service_id":       serviceID,
			"expected_content": expectedContent,
		},
	})
	return res.Changed, err
}

func (c *ComposeClient) composeAction(path, serviceID string) error {
	slog.Info("agent request", "path", path, "service", serviceID)

	_, err := c.call("agent "+path, agentReq{
		path:    path,
		payload: agentServiceReq{ServiceID: serviceID},
	})
	return err
}
