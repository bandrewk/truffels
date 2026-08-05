package alerts

import "time"

// reclaimInput is everything decideReclaim needs. Kept as plain data so the
// decision is testable without Docker, a store, or a real clock — same shape
// as the trend evaluation in trend.go.
type reclaimInput struct {
	SizeBytes     int64
	CriticalBytes int64

	Enabled        bool
	MinInterval    time.Duration
	LastReclaim    time.Time
	HasLastReclaim bool
	Now            time.Time

	// False when no container of the service is running. Also covers initial
	// block download implicitly: during IBD electrs is not healthy, so mempool
	// is not running either.
	ServiceRunning bool

	// Mirrored from the service template's DataDir entry. Both must be true;
	// this is what keeps the reclaim off directories like mempool/mysql.
	TargetClearable    bool
	TargetRequiresStop bool
}

// reclaimDecision carries the verdict and, when refusing, why. Reason is
// written to the debug log so an operator can tell "not triggered" from
// "triggered but blocked".
type reclaimDecision struct {
	Act    bool
	Reason string
}

// Refusal reasons. Named because they are matched, not just logged:
// reclaimTail turns them into the operator-facing tail of the
// dir_size_critical alert, and a silent string edit would silently change
// what the alert tells the operator to do.
const (
	reasonNotClearable      = "target dir is not marked clearable+requires-stop"
	reasonDisabled          = "auto-reclaim disabled by setting"
	reasonBelowThreshold    = "below critical threshold"
	reasonNotRunning        = "service is not running"
	reasonCooldown          = "inside minimum interval since last reclaim"
	reasonNoTemplate        = "no service template for the watched dir"
	reasonNotDeclared       = "watched dir is not declared in the service template"
	reasonAuditLookupFailed = "audit lookup failed"
)

// decideReclaim applies every guardrail in a fixed order. The safety checks
// come first so a misconfigured target can never reach the size comparison.
func decideReclaim(in reclaimInput) reclaimDecision {
	if !in.TargetClearable || !in.TargetRequiresStop {
		return reclaimDecision{false, reasonNotClearable}
	}
	if !in.Enabled {
		return reclaimDecision{false, reasonDisabled}
	}
	if in.SizeBytes < in.CriticalBytes {
		return reclaimDecision{false, reasonBelowThreshold}
	}
	if !in.ServiceRunning {
		return reclaimDecision{false, reasonNotRunning}
	}
	if in.HasLastReclaim && in.Now.Sub(in.LastReclaim) < in.MinInterval {
		return reclaimDecision{false, reasonCooldown}
	}
	return reclaimDecision{true, ""}
}

// reclaimTail is the tail of the dir_size_critical alert message. It is
// derived from the same verdict that drives the action, so the alert can no
// longer promise an automatic reclaim while a guardrail is refusing one —
// previously the tail was chosen from the enabled flag alone, which meant
// that after a failed reclaim the critical alert kept telling the operator
// "it will be cleared automatically" for the whole cooldown window while
// nothing was going to happen, suppressing the manual clear that was by then
// the only remedy.
func reclaimTail(d reclaimDecision) string {
	const manual = "Stop the service and clear the dir via Settings → Data Dirs."
	switch {
	case d.Act:
		return "It will be cleared automatically and the service restarted (~60 s)."
	case d.Reason == reasonCooldown:
		return "An automatic reclaim already ran recently and will not run again until the configured minimum interval has elapsed. " + manual
	case d.Reason == reasonNotRunning:
		return "Automatic reclaim only runs while the service is running — start the service, or clear the dir via Settings → Data Dirs."
	default:
		// Disabled, a target that is not clearable, a missing template, an
		// unreadable audit log: in every case the operator is the only actor
		// left, so say so plainly rather than naming an internal reason.
		return manual
	}
}
