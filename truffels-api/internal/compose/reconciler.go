package compose

import (
	"fmt"
	"log/slog"
	"time"

	"truffels-api/internal/docker"
	"truffels-api/internal/service"
)

// serviceIDs that have compose templates (skip truffels — self-managed).
var reconciledServices = []string{"bitcoind", "electrs", "ckpool", "mempool", "ckstats", "proxy"}

type Reconciler struct {
	registry *service.Registry
	compose  *docker.ComposeClient
}

func NewReconciler(registry *service.Registry, compose *docker.ComposeClient) *Reconciler {
	return &Reconciler{
		registry: registry,
		compose:  compose,
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

	// Compare and write if different
	changed, err := r.reconcileWithRetry(serviceID, expected)
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}

	if !changed {
		slog.Info("compose unchanged", "service", serviceID)
		return nil
	}

	slog.Info("compose reconciled, restarting", "service", serviceID)
	if err := r.compose.Up(serviceID); err != nil {
		return fmt.Errorf("restart after reconcile: %w", err)
	}

	return nil
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
