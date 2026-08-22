// Package syncstatus holds the chain-node sync progress that the Services page
// shows. The alerts engine refreshes it on its background tick; the API handler
// reads it. This keeps the live chain probe (a docker exec into an IBD-busy
// node, which can take many seconds) off the request path — the page reads a
// cached value instead of blocking on the slowest node.
package syncstatus

import (
	"strconv"
	"sync"

	"truffels-api/internal/model"
)

// Cache maps a service ID to its last known sync progress. A nil value (or a
// missing key) means "synced or unknown" — the same absence-of-SyncInfo the
// handler produced when it probed live and the node was fully synced.
//
// One writer (the engine tick) and many readers (HTTP handlers), so a RWMutex
// is the right primitive. Values are treated as immutable once stored; readers
// get a copy so a later Set can never mutate a pointer a caller still holds.
type Cache struct {
	mu sync.RWMutex
	m  map[string]*model.SyncInfo
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{m: make(map[string]*model.SyncInfo)}
}

// Set stores (or replaces) the sync info for a service. Pass nil to record that
// the node is synced / has no progress to show.
func (c *Cache) Set(id string, info *model.SyncInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[id] = info
}

// Get returns a copy of the cached sync info, or nil if the service has no
// entry or is recorded as synced.
func (c *Cache) Get(id string) *model.SyncInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	info, ok := c.m[id]
	if !ok || info == nil {
		return nil
	}
	cp := *info
	return &cp
}

// FromChainStatus builds the SyncInfo a bitcoin-core-style node reports, or nil
// when the node is fully synced (verificationprogress ~1 and IBD false) — the
// same rule the handler used when it probed live.
func FromChainStatus(blocks, headers int64, verificationProgress float64, initialBlockDownload bool) *model.SyncInfo {
	if !initialBlockDownload && verificationProgress >= 0.9999 {
		return nil
	}
	return &model.SyncInfo{
		Syncing:  true,
		Progress: verificationProgress,
		Detail:   formatProgress(verificationProgress, blocks, headers),
	}
}

func formatProgress(vp float64, blocks, headers int64) string {
	return strconv.FormatFloat(vp*100, 'f', 2, 64) + "% (" +
		formatInt(blocks) + " / " + formatInt(headers) + " blocks)"
}

// formatInt renders an integer with thousands separators, matching the
// formatting the API handler used for the same field.
func formatInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	if len(s) <= 3 {
		return neg + s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, s[i])
	}
	return neg + string(b)
}
