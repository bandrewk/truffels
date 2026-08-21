package compose

import (
	"errors"
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
// "truffels" is FIRST and dispatches to ComposeUpDetached when it needs a
// restart — the agent writes a shell script to host /tmp and execs it via
// nsenter, so the restart survives the API process exiting. By running it
// first, the agent gets its new mounts (e.g. /srv/truffels/config:rw) before
// any downstream service that depends on those mounts (e.g. proxy writing
// the Caddyfile). On a detected truffels-compose drift we short-circuit the
// loop entirely — the next API boot picks up the rest of the services.
var reconciledServices = []string{"truffels", "bitcoind", "electrs", "ckpool", "mempool", "ckstats", "proxy"}

// AlertStore is the subset of the store interface the reconciler needs to
// surface reconciliation failures as alerts. Defined here (not imported) to
// avoid an import cycle with internal/store.
type AlertStore interface {
	UpsertAlert(*model.Alert) error
	ResolveAlerts(alertType, serviceID string) error
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
// errShortCircuit signals that the reconciler dispatched a detached restart
// (truffels stack) — the API is about to be replaced, so the remaining
// services should not be reconciled in this cycle. The next API boot picks
// them up.
var errShortCircuit = fmt.Errorf("reconciler: short-circuiting after detached restart")

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

		err := r.reconcileService(serviceID)
		if err == nil {
			continue
		}
		if errors.Is(err, errShortCircuit) {
			slog.Info("reconciliation short-circuited; next boot will resume",
				"after_service", serviceID)
			return nil
		}
		slog.Warn("compose reconciliation failed", "service", serviceID, "err", err)
		errs = append(errs, fmt.Errorf("%s: %w", serviceID, err))
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
		caddyfile := RenderCaddyfile(WebRoutesFrom(r.registry))
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
		if serviceID == "truffels" {
			// The detached script is about to recreate the API. Any further
			// reconciliation in this cycle would hit either the dying agent
			// (connection refused) or the about-to-be-replaced agent (still
			// using OLD mounts). The next API boot does a clean pass.
			r.clearAlert(serviceID)
			return errShortCircuit
		}
	}

	// Reconcile Dockerfile if this service has one in the repo
	if repoPath, ok := dockerfileMapping[serviceID]; ok {
		r.reconcileDockerfile(serviceID, repoPath)
	}

	// All steps succeeded — clear any stale compose_reconcile_failed alert
	// from a prior failed cycle so the UI doesn't show a phantom problem.
	r.clearAlert(serviceID)
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

// clearAlert resolves any open compose_reconcile_failed alert for this
// service. Called when reconciliation completes successfully so a stale
// alert from a prior failed cycle doesn't linger forever in the UI.
func (r *Reconciler) clearAlert(serviceID string) {
	if r.alertStore == nil {
		return
	}
	if err := r.alertStore.ResolveAlerts("compose_reconcile_failed", serviceID); err != nil {
		slog.Warn("failed to resolve stale reconcile alert", "service", serviceID, "err", err)
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
