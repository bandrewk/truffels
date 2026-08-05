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

// decideReclaim applies every guardrail in a fixed order. The safety checks
// come first so a misconfigured target can never reach the size comparison.
func decideReclaim(in reclaimInput) reclaimDecision {
	if !in.TargetClearable || !in.TargetRequiresStop {
		return reclaimDecision{false, "target dir is not marked clearable+requires-stop"}
	}
	if !in.Enabled {
		return reclaimDecision{false, "auto-reclaim disabled by setting"}
	}
	if in.SizeBytes < in.CriticalBytes {
		return reclaimDecision{false, "below critical threshold"}
	}
	if !in.ServiceRunning {
		return reclaimDecision{false, "service is not running"}
	}
	if in.HasLastReclaim && in.Now.Sub(in.LastReclaim) < in.MinInterval {
		return reclaimDecision{false, "inside minimum interval since last reclaim"}
	}
	return reclaimDecision{true, ""}
}
