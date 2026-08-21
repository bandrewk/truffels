package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Default per-operation timeouts. Reads are quick; apply writes files; remove
// runs `compose down`, which for a chain node flushing state can take a while.
//
// removeTimeout is deliberately longer than the agent's own compose timeout
// (5 min in runCompose). The agent's timeout must be the one that fires first:
// if the client gives up while the agent is still tearing the service down, the
// caller sees a failure and rolls back the install record even though the agent
// then finishes — leaving a phantom "installed" row with nothing on disk.
const (
	defaultReadTimeout   = 15 * time.Second
	defaultApplyTimeout  = 60 * time.Second
	defaultRemoveTimeout = 6 * time.Minute
)

type Client struct {
	agentURL string
	http     *http.Client

	// Per-operation timeouts; overridable in tests.
	readTimeout   time.Duration
	applyTimeout  time.Duration
	removeTimeout time.Duration

	mu     sync.RWMutex
	cached map[string]Entry
}

func NewClient(agentURL string) *Client {
	// No client-wide timeout: each call sets its own via a request context, so a
	// slow teardown is not capped by the same deadline as a quick catalog read.
	return &Client{
		agentURL:      agentURL,
		http:          &http.Client{},
		readTimeout:   defaultReadTimeout,
		applyTimeout:  defaultApplyTimeout,
		removeTimeout: defaultRemoveTimeout,
	}
}

// postJSON POSTs a JSON body to the agent with a per-call timeout.
func (c *Client) postJSON(path string, body []byte, timeout time.Duration) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	// The caller closes resp.Body; cancel must outlive the read, so defer it there.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.agentURL+path, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose ties a context's cancel func to the response body's lifetime so
// the deadline covers reading the body, not just receiving the headers.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	c.cancel()
	return c.ReadCloser.Close()
}

func (c *Client) All() (map[string]Entry, error) {
	c.mu.RLock()
	if c.cached != nil {
		defer c.mu.RUnlock()
		return c.cached, nil
	}
	c.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), c.readTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.agentURL+"/v1/catalog", nil)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch catalog: status %d", resp.StatusCode)
	}
	var out map[string]Entry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}

	c.mu.Lock()
	c.cached = out
	c.mu.Unlock()
	return out, nil
}

func (c *Client) Get(id string) (Entry, bool) {
	all, err := c.All()
	if err != nil {
		return Entry{}, false
	}
	e, ok := all[id]
	return e, ok
}

// Invalidate discards the cache. Call this after the agent has been restarted
// — for example after a self-update of the truffels stack.
func (c *Client) Invalidate() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
}

// IDs returns a map of all catalog service IDs. The map keys are the IDs and
// values are always true for use as a set.
func (c *Client) IDs() (map[string]bool, error) {
	all, err := c.All()
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool)
	for id := range all {
		ids[id] = true
	}
	return ids, nil
}

func (c *Client) Apply(id string, params map[string]any) error {
	type req struct {
		ID     string         `json:"id"`
		Params map[string]any `json:"params"`
	}
	body, err := json.Marshal(req{ID: id, Params: params})
	if err != nil {
		return err
	}
	resp, err := c.postJSON("/v1/service/apply", body, c.applyTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("agent apply status %d", resp.StatusCode)
	}
	return nil
}

// ChainSyncStatus is the bitcoin-core-style subset of getblockchaininfo the
// API needs to render a sync indicator.
type ChainSyncStatus struct {
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
}

// ChainProbe asks the agent to run a catalog chain node's sync probe inside its
// container and returns the parsed status. The probe command lives in the
// embedded catalog on the agent side; the API only names the service.
func (c *Client) ChainProbe(id string) (*ChainSyncStatus, error) {
	body, err := json.Marshal(struct {
		ID string `json:"id"`
	}{ID: id})
	if err != nil {
		return nil, err
	}
	resp, err := c.postJSON("/v1/service/chain-probe", body, c.readTimeout)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent chain-probe status %d", resp.StatusCode)
	}
	var wrap struct {
		Output string `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrap); err != nil {
		return nil, fmt.Errorf("decode chain-probe: %w", err)
	}
	var status ChainSyncStatus
	if err := json.Unmarshal([]byte(wrap.Output), &status); err != nil {
		return nil, fmt.Errorf("parse chain-probe output: %w", err)
	}
	return &status, nil
}

func (c *Client) Remove(id string, purgeData bool) error {
	type req struct {
		ID        string `json:"id"`
		PurgeData bool   `json:"purge_data"`
	}
	body, err := json.Marshal(req{ID: id, PurgeData: purgeData})
	if err != nil {
		return err
	}
	resp, err := c.postJSON("/v1/service/remove", body, c.removeTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("agent remove status %d", resp.StatusCode)
	}
	return nil
}
