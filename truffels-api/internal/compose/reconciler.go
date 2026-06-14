package compose

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"truffels-api/internal/docker"
	"truffels-api/internal/model"
	"truffels-api/internal/service"
)

// dockerfileMapping maps service IDs to their Dockerfile path in the repo (relative to /repo/).
var dockerfileMapping = map[string]string{
	"ckstats": "dockerfiles/ckstats/Dockerfile",
	"ckpool":  "dockerfiles/ckpool/Dockerfile",
}

// serviceIDs that have compose templates.
//
// "truffels" is special: its compose contains the API itself, so a normal
// `compose up` would kill the running reconciler. The reconcileService path
// dispatches to ComposeUpDetached for that ID — the agent writes a shell
// script to host /tmp and execs it via nsenter, so the restart survives the
// API process exiting.
var reconciledServices = []string{"bitcoind", "electrs", "ckpool", "mempool", "ckstats", "proxy", "truffels"}

// AlertStore is the subset of the store interface the reconciler needs to
// surface reconciliation failures as alerts. Defined here (not imported) to
// avoid an import cycle with internal/store.
type AlertStore interface {
	UpsertAlert(*model.Alert) error
}

type Reconciler struct {
	registry   *service.Registry
	compose    *docker.ComposeClient
	alertStore AlertStore // optional — if nil, failures are slog-only
}

func NewReconciler(registry *service.Registry, compose *docker.ComposeClient, alertStore AlertStore) *Reconciler {
	return &Reconciler{
		registry:   registry,
		compose:    compose,
		alertStore: alertStore,
	}
}

// Run reads each managed compose file, renders the expected content from templates,
// and writes + restarts if different. Retries on agent connection failures.
func (r *Reconciler) Run() error {
	seen := map[string]bool{}
	var errs []error

	for _, serviceID := range reconciledServices {
		tmpl, ok := r.registry.Get(serviceID)
		if !ok {
			continue
		}

		// Skip duplicate compose dirs (e.g. mempool-db shares mempool's dir)
		if seen[tmpl.ComposeDir] {
			continue
		}
		seen[tmpl.ComposeDir] = true

		if err := r.reconcileService(serviceID); err != nil {
			slog.Warn("compose reconciliation failed", "service", serviceID, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", serviceID, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%d reconciliation error(s): %v", len(errs), errs[0])
	}
	return nil
}

func (r *Reconciler) reconcileService(serviceID string) error {
	// Read current compose file (with retry for agent startup)
	content, err := r.readWithRetry(serviceID)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	// Extract current image tags
	params, err := ExtractParams(serviceID, content)
	if err != nil {
		return fmt.Errorf("extract params: %w", err)
	}

	// Render expected content from template
	expected, err := Render(serviceID, params)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	// Ensure any declared bind-mount host directories exist with correct
	// ownership before we hand the new compose to `docker compose up`.
	// Non-fatal: a failure here gets a critical alert and a warning log but
	// we still proceed to write the compose. The service will then fail loud
	// (caught by healthcheck + restart-loop autostop from dev.14) rather than
	// silently skipping the entire reconciliation.
	tmpl, _ := r.registry.Get(serviceID)
	for _, d := range tmpl.EnsureDirs {
		if err := r.compose.FsEnsureDir(d.Path, d.UID, d.GID, d.Mode); err != nil {
			slog.Warn("ensure-dir failed; continuing with compose write",
				"service", serviceID, "path", d.Path, "err", err)
			r.raiseAlert(serviceID, fmt.Sprintf("ensure-dir %s failed: %v", d.Path, err))
			continue
		}
		slog.Info("ensure-dir ok", "service", serviceID, "path", d.Path)
	}

	// Proxy ships a Caddyfile alongside the compose YAML. Reconcile it via
	// the same agent file/reconcile path so structural Caddy config changes
	// flow through the update channel.
	if serviceID == "proxy" {
		caddyfile := RenderCaddyfile()
		caddyfilePath := "/srv/truffels/config/proxy/Caddyfile"
		if err := r.reconcileFileWithRetry(caddyfilePath, caddyfile); err != nil {
			slog.Warn("Caddyfile reconcile failed; proceeding with compose",
				"err", err)
			r.raiseAlert("proxy", fmt.Sprintf("Caddyfile reconcile failed: %v", err))
		}
	}

	// Compare and write if different
	changed, err := r.reconcileWithRetry(serviceID, expected)
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	if !changed {
		slog.Info("compose unchanged", "service", serviceID)
	} else {
		slog.Info("compose reconciled, restarting", "service", serviceID)
		var upErr error
		if serviceID == "truffels" {
			// Detached restart — agent execs the up script in PID 1's namespace
			// so the API can be killed mid-reconcile without orphaning the stack.
			upErr = r.compose.ComposeUpDetached(serviceID)
		} else {
			upErr = r.compose.Up(serviceID)
		}
		if upErr != nil {
			// Fail loud — without this, a typo in the new compose template
			// silently breaks the service while self-update reports success.
			r.raiseAlert(serviceID, fmt.Sprintf("compose reconcile restart failed: %v", upErr))
			return fmt.Errorf("restart after reconcile: %w", upErr)
		}
	}

	// Reconcile Dockerfile if this service has one in the repo
	if repoPath, ok := dockerfileMapping[serviceID]; ok {
		r.reconcileDockerfile(serviceID, repoPath)
	}

	return nil
}

// raiseAlert surfaces a reconciliation failure as a critical alert. If no
// alertStore is configured (e.g. in tests), falls back to slog.
func (r *Reconciler) raiseAlert(serviceID, msg string) {
	if r.alertStore == nil {
		return
	}
	if err := r.alertStore.UpsertAlert(&model.Alert{
		Type:      "compose_reconcile_failed",
		Severity:  model.SeverityCritical,
		ServiceID: serviceID,
		Message:   msg,
	}); err != nil {
		slog.Error("failed to upsert reconcile alert", "service", serviceID, "err", err)
	}
}

func (r *Reconciler) reconcileDockerfile(serviceID, repoPath string) {
	expected, err := os.ReadFile("/repo/" + repoPath)
	if err != nil {
		slog.Warn("dockerfile read from repo failed", "service", serviceID, "err", err)
		return
	}

	changed, err := r.compose.ReconcileFile(serviceID+"/Dockerfile", string(expected))
	if err != nil {
		slog.Warn("dockerfile reconcile failed", "service", serviceID, "err", err)
		return
	}

	if changed {
		slog.Info("dockerfile reconciled", "service", serviceID)
	} else {
		slog.Info("dockerfile unchanged", "service", serviceID)
	}
}

func (r *Reconciler) readWithRetry(serviceID string) (string, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		content, err := r.compose.ComposeRead(serviceID)
		if err == nil {
			return content, nil
		}
		lastErr = err
		slog.Warn("compose read retry", "service", serviceID, "attempt", i+1, "err", err)
		time.Sleep(5 * time.Second)
	}
	return "", lastErr
}

func (r *Reconciler) reconcileWithRetry(serviceID, expected string) (bool, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		changed, err := r.compose.ComposeReconcile(serviceID, expected)
		if err == nil {
			return changed, nil
		}
		lastErr = err
		slog.Warn("compose reconcile retry", "service", serviceID, "attempt", i+1, "err", err)
		time.Sleep(5 * time.Second)
	}
	return false, lastErr
}

func (r *Reconciler) reconcileFileWithRetry(path, content string) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		changed, err := r.compose.FileReconcile(path, content)
		if err == nil {
			if changed {
				slog.Info("config file reconciled", "path", path)
			}
			return nil
		}
		lastErr = err
		slog.Warn("file reconcile retry", "path", path, "attempt", i+1, "err", err)
		time.Sleep(5 * time.Second)
	}
	return lastErr
}
