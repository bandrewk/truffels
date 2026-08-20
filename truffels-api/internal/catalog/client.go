package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Client struct {
	agentURL string
	http     *http.Client

	mu     sync.RWMutex
	cached map[string]Entry
}

func NewClient(agentURL string) *Client {
	return &Client{
		agentURL: agentURL,
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) All() (map[string]Entry, error) {
	c.mu.RLock()
	if c.cached != nil {
		defer c.mu.RUnlock()
		return c.cached, nil
	}
	c.mu.RUnlock()

	resp, err := c.http.Get(c.agentURL + "/v1/catalog")
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
	resp, err := c.http.Post(c.agentURL+"/v1/service/apply", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("agent apply status %d", resp.StatusCode)
	}
	return nil
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
	resp, err := c.http.Post(c.agentURL+"/v1/service/remove", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("agent remove status %d", resp.StatusCode)
	}
	return nil
}
