package alerts

import (
	"testing"
	"time"
)

// baseInput is a case that should reclaim. Each test mutates one field so a
// failure names exactly one guardrail.
func baseInput() reclaimInput {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	return reclaimInput{
		SizeBytes:          950 * 1024 * 1024,
		CriticalBytes:      900 * 1024 * 1024,
		Enabled:            true,
		MinInterval:        24 * time.Hour,
		LastReclaim:        now.Add(-48 * time.Hour),
		HasLastReclaim:     true,
		Now:                now,
		ServiceRunning:     true,
		TargetClearable:    true,
		TargetRequiresStop: true,
	}
}

func TestDecideReclaim_ActsWhenAllConditionsMet(t *testing.T) {
	d := decideReclaim(baseInput())
	if !d.Act {
		t.Fatalf("expected Act=true, got false (%s)", d.Reason)
	}
}

func TestDecideReclaim_Guardrails(t *testing.T) {
	now := baseInput().Now
	cases := []struct {
		name   string
		mutate func(*reclaimInput)
	}{
		{"below threshold", func(i *reclaimInput) { i.SizeBytes = 800 * 1024 * 1024 }},
		{"disabled", func(i *reclaimInput) { i.Enabled = false }},
		{"inside min interval", func(i *reclaimInput) { i.LastReclaim = now.Add(-1 * time.Hour) }},
		{"service not running", func(i *reclaimInput) { i.ServiceRunning = false }},
		{"target not clearable", func(i *reclaimInput) { i.TargetClearable = false }},
		{"target not requires-stop", func(i *reclaimInput) { i.TargetRequiresStop = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput()
			tc.mutate(&in)
			d := decideReclaim(in)
			if d.Act {
				t.Errorf("expected Act=false for %q", tc.name)
			}
			if d.Reason == "" {
				t.Error("expected a non-empty Reason when refusing")
			}
		})
	}
}

// A first-ever reclaim has no prior audit entry and must not be blocked.
func TestDecideReclaim_NoPriorReclaimIsAllowed(t *testing.T) {
	in := baseInput()
	in.HasLastReclaim = false
	in.LastReclaim = time.Time{}
	if d := decideReclaim(in); !d.Act {
		t.Fatalf("expected Act=true on first reclaim, got %s", d.Reason)
	}
}
