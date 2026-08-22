package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// oomEvent records one container OOM-kill observed on the Docker event stream.
type oomEvent struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Time int64  `json:"time"` // unix seconds
}

// oomTracker keeps a bounded, time-limited history of recent OOM-kill events.
//
// The Docker daemon emits an `oom` event the moment the cgroup OOM-killer kills
// a container's process — reliably, and independent of the restart policy. It
// does NOT, however, retain past events long enough for periodic polling to
// catch them (a container that OOMs and is auto-restarted shows OOMKilled=false
// on the next inspect). So the agent streams `docker events` continuously and
// records the OOM actions here for the control plane to poll.
type oomTracker struct {
	mu     sync.Mutex
	events []oomEvent
}

const (
	oomRetention = 60 * time.Minute
	oomMaxEvents = 100
)

func (t *oomTracker) add(e oomEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
	if len(t.events) > oomMaxEvents {
		t.events = t.events[len(t.events)-oomMaxEvents:]
	}
}

// recent returns a copy of the events newer than oomRetention, pruning older
// ones from the store as a side effect.
func (t *oomTracker) recent(now int64) []oomEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := now - int64(oomRetention/time.Second)
	kept := make([]oomEvent, 0, len(t.events))
	for _, e := range t.events {
		if e.Time >= cutoff {
			kept = append(kept, e)
		}
	}
	t.events = kept
	out := make([]oomEvent, len(kept))
	copy(out, kept)
	return out
}

var oomState = &oomTracker{}

// dockerEvent is the subset of `docker events --format {{json .}}` we read.
type dockerEvent struct {
	Time  int64 `json:"time"`
	Actor struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

// watchOOMEvents streams container OOM events from the Docker daemon and records
// them in oomState, reconnecting if the stream drops (e.g. daemon restart). It
// is read-only: it only consumes the event stream over the Docker socket the
// agent already holds — no host access, no mutation.
func watchOOMEvents(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		streamOOMOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func streamOOMOnce(ctx context.Context) {
	cmd := exec.CommandContext(ctx, "docker", "events",
		"--filter", "type=container", "--filter", "event=oom",
		"--format", "{{json .}}")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		slog.Error("oom watch: stdout pipe", "err", err)
		return
	}
	if err := cmd.Start(); err != nil {
		slog.Error("oom watch: start", "err", err)
		return
	}
	scanner := bufio.NewScanner(stdout)
	// A container with a very large label/attribute set could emit an event
	// line past bufio's default 64 KiB token cap; without a larger buffer that
	// would end the scan and trigger a reconnect loop. 1 MiB is far beyond any
	// realistic event line.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ev dockerEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		e := oomEvent{
			Name: ev.Actor.Attributes["name"],
			ID:   ev.Actor.ID,
			Time: ev.Time,
		}
		if e.Time == 0 {
			e.Time = time.Now().Unix()
		}
		oomState.add(e)
		slog.Warn("container OOM-killed", "container", e.Name, "id", e.ID)
	}
	// Wait reaps the process; its error is expected on ctx cancel / stream end.
	_ = cmd.Wait()
}

// handleOOMEvents returns the OOM-kill events seen in the last hour.
func handleOOMEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"events": oomState.recent(time.Now().Unix())})
}
