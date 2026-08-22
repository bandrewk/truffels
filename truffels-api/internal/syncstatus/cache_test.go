package syncstatus

import (
	"testing"

	"truffels-api/internal/model"
)

func TestCacheSetGet(t *testing.T) {
	c := NewCache()

	if got := c.Get("missing"); got != nil {
		t.Errorf("missing key should return nil, got %+v", got)
	}

	info := &model.SyncInfo{Syncing: true, Progress: 0.5, Detail: "50%"}
	c.Set("bchn", info)

	got := c.Get("bchn")
	if got == nil || !got.Syncing || got.Progress != 0.5 {
		t.Fatalf("expected cached info, got %+v", got)
	}
	// Get must return a copy, not the stored pointer.
	if got == info {
		t.Error("Get returned the stored pointer; must return a copy")
	}
	got.Progress = 0.9
	if again := c.Get("bchn"); again.Progress != 0.5 {
		t.Errorf("mutating a returned copy changed the cache: %v", again.Progress)
	}

	// Storing nil records "synced / no info".
	c.Set("bchn", nil)
	if got := c.Get("bchn"); got != nil {
		t.Errorf("nil entry should read back as nil, got %+v", got)
	}
}

func TestFromChainStatus(t *testing.T) {
	// Fully synced → nil (no progress bar).
	if got := FromChainStatus(800000, 800000, 1.0, false); got != nil {
		t.Errorf("synced node should produce nil SyncInfo, got %+v", got)
	}
	if got := FromChainStatus(800000, 800000, 0.99995, false); got != nil {
		t.Errorf("vp>=0.9999 and not-IBD should be nil, got %+v", got)
	}

	// Still in IBD → SyncInfo with formatted detail.
	got := FromChainStatus(226256, 965199, 0.0348, true)
	if got == nil || !got.Syncing {
		t.Fatal("IBD node should produce a syncing SyncInfo")
	}
	if got.Progress != 0.0348 {
		t.Errorf("progress = %v, want 0.0348", got.Progress)
	}
	if got.Detail != "3.48% (226,256 / 965,199 blocks)" {
		t.Errorf("detail = %q", got.Detail)
	}
}
