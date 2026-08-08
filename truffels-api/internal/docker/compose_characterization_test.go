package docker

// Characterization tests for ComposeClient.
//
// These tests describe the behaviour of compose.go AS IT IS, not as it ought to
// be. They were written before the client was refactored onto shared request
// helpers, specifically so that the refactor could be proven behaviour-
// preserving: the file is expected to stay byte-identical across that change.
//
// That means a few of the assertions below deliberately pin behaviour that
// looks wrong:
//
//   - SystemInfoGet and HostDirSize never inspect the status code, so a 500 that
//     still carries a JSON body is reported as success.
//   - Logs and SystemJournal return the agent's partial log text alongside the
//     error; Pull and ComposeRead return "" in the same situation.
//   - The reconcile/prune/read family reports only ar.Error and drops the
//     agent's output, while everything else routes through agentError and keeps
//     it.
//
// If one of these has to change, that is a behaviour change and belongs in its
// own commit with its own reasoning — not a silent edit during a cleanup.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// --- harness ---------------------------------------------------------------

// recordedRequest captures what the fake agent received.
type recordedRequest struct {
	path     string
	method   string
	rawQuery string
	body     []byte
}

// newFakeAgent starts a stub agent that records the request it gets and answers
// with the given status and JSON body. Returns a client pointed at it.
func newFakeAgent(t *testing.T, status int, respBody any) (*ComposeClient, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.path = r.URL.Path
		rec.method = r.Method
		rec.rawQuery = r.URL.RawQuery
		rec.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(respBody)
	}))
	t.Cleanup(srv.Close)
	return NewComposeClient(srv.URL), rec
}

// newRawAgent starts a stub agent answering with a literal body, for the
// malformed-JSON cases.
func newRawAgent(t *testing.T, status int, raw string) *ComposeClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, raw)
	}))
	t.Cleanup(srv.Close)
	return NewComposeClient(srv.URL)
}

// unreachableAgent points the client at a port nothing listens on, so every
// call fails at the transport layer.
func unreachableAgent() *ComposeClient {
	return NewComposeClient("http://127.0.0.1:1")
}

// assertJSONBody compares a recorded request body against an expected value,
// normalising both through JSON so that numeric types and key order do not
// matter.
func assertJSONBody(t *testing.T, got []byte, want any) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	var gotAny, wantAny any
	if err := json.Unmarshal(got, &gotAny); err != nil {
		t.Fatalf("request body is not valid JSON: %v (%q)", err, got)
	}
	if err := json.Unmarshal(wantJSON, &wantAny); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if !reflect.DeepEqual(gotAny, wantAny) {
		t.Errorf("request body mismatch\n got: %s\nwant: %s", got, wantJSON)
	}
}

func assertContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err, want)
	}
}

// The agent's diagnosis marker: methods that route failures through agentError
// must carry it into the returned error, the others must not.
const diagnosisMarker = "DIAGNOSIS-FROM-AGENT-OUTPUT"

func failureBody() map[string]string {
	return map[string]string{
		"error":  "op failed: exit status 1",
		"output": diagnosisMarker,
	}
}

// --- constructor -----------------------------------------------------------

func TestCharacterize_NewComposeClient(t *testing.T) {
	c := NewComposeClient("http://agent:9000")
	if c.agentURL != "http://agent:9000" {
		t.Errorf("agentURL = %q", c.agentURL)
	}
	if c.httpClient == nil {
		t.Fatal("httpClient is nil")
	}
	// The shared client's timeout is the default for every call that does not
	// explicitly ask for a longer one.
	if c.httpClient.Timeout != 6*time.Minute {
		t.Errorf("default timeout = %v, want 6m", c.httpClient.Timeout)
	}
}

// --- POST, 200 expected, error-only result, agentError on failure ----------

// errorOnlyCase describes one method whose entire contract is "POST this body
// to this path; return nil on 200, an agentError otherwise".
type errorOnlyCase struct {
	name     string
	wantPath string
	wantBody any
	// wantOp is the exact prefix of the returned error, on both the agent-error
	// and the transport-error path. These strings reach the update log and the
	// UI, so they are part of the observable surface.
	wantOp string
	call   func(*ComposeClient) error
}

func errorOnlyCases() []errorOnlyCase {
	return []errorOnlyCase{
		{
			name: "Up", wantPath: "/v1/compose/up",
			wantBody: map[string]any{"service_id": "bitcoind"},
			wantOp:   "agent /v1/compose/up",
			call:     func(c *ComposeClient) error { return c.Up("bitcoind") },
		},
		{
			name: "Down", wantPath: "/v1/compose/down",
			wantBody: map[string]any{"service_id": "bitcoind"},
			wantOp:   "agent /v1/compose/down",
			call:     func(c *ComposeClient) error { return c.Down("bitcoind") },
		},
		{
			name: "Stop", wantPath: "/v1/compose/stop",
			wantBody: map[string]any{"service_id": "electrs"},
			wantOp:   "agent /v1/compose/stop",
			call:     func(c *ComposeClient) error { return c.Stop("electrs") },
		},
		{
			name: "Restart", wantPath: "/v1/compose/restart",
			wantBody: map[string]any{"service_id": "electrs"},
			wantOp:   "agent /v1/compose/restart",
			call:     func(c *ComposeClient) error { return c.Restart("electrs") },
		},
		{
			name: "ImageTag", wantPath: "/v1/image/tag",
			wantBody: map[string]any{"source": "img:new", "target": "img:rollback"},
			wantOp:   "agent image tag",
			call:     func(c *ComposeClient) error { return c.ImageTag("img:new", "img:rollback") },
		},
		{
			name: "Build", wantPath: "/v1/compose/build",
			wantBody: map[string]any{"service_id": "truffels-api"},
			wantOp:   "agent build",
			call:     func(c *ComposeClient) error { return c.Build("truffels-api") },
		},
		{
			// The op string interpolates the action, so shutdown and restart
			// produce different error prefixes.
			name: "SystemAction", wantPath: "/v1/system/restart",
			wantBody: map[string]any{"action": "restart"},
			wantOp:   "agent system restart",
			call:     func(c *ComposeClient) error { return c.SystemAction("restart") },
		},
		{
			name: "SystemTuningSet", wantPath: "/v1/system/tuning",
			wantBody: map[string]any{"action": "swappiness", "value": "10"},
			wantOp:   "agent tuning set",
			call:     func(c *ComposeClient) error { return c.SystemTuningSet("swappiness", "10") },
		},
		{
			name: "GitCheckout", wantPath: "/v1/git/checkout",
			wantBody: map[string]any{"repo_dir": "/repo", "tag": "abc123", "ref_scheme": "commit"},
			wantOp:   "agent git checkout",
			call:     func(c *ComposeClient) error { return c.GitCheckout("/repo", "abc123", "commit") },
		},
		{
			// Note the op is "agent build", not "agent build with args" — it
			// shares Build's error prefix.
			name: "BuildWithArgs", wantPath: "/v1/compose/build",
			wantBody: map[string]any{
				"service_id": "truffels-web",
				"build_args": map[string]any{"VERSION": "v0.3.0"},
				"services":   []any{"web"},
			},
			wantOp: "agent build",
			call: func(c *ComposeClient) error {
				return c.BuildWithArgs("truffels-web", map[string]string{"VERSION": "v0.3.0"}, "web")
			},
		},
		{
			name: "RewriteTags", wantPath: "/v1/compose/rewrite-tags",
			wantBody: map[string]any{
				"service_id": "mempool",
				"images":     []any{"mempool/backend"},
				"new_tag":    "v3.2.1",
				"old_tag":    "v3.2.0",
			},
			wantOp: "agent rewrite tags",
			call: func(c *ComposeClient) error {
				return c.RewriteTags("mempool", []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
			},
		},
		{
			name: "RemoveImage", wantPath: "/v1/image/remove",
			wantBody: map[string]any{"image": "old:tag"},
			wantOp:   "agent remove image",
			call:     func(c *ComposeClient) error { return c.RemoveImage("old:tag") },
		},
		{
			name: "FsEnsureDir", wantPath: "/v1/fs/ensure-dir",
			wantBody: map[string]any{"path": "bitcoin", "uid": 1000, "gid": 1000, "mode": "0750"},
			wantOp:   "agent ensure-dir",
			call:     func(c *ComposeClient) error { return c.FsEnsureDir("bitcoin", 1000, 1000, "0750") },
		},
		{
			name: "FsClearDir", wantPath: "/v1/fs/clear-dir",
			wantBody: map[string]any{"path": "electrs", "uid": 1000, "gid": 1000, "mode": "0755"},
			wantOp:   "agent clear-dir",
			call:     func(c *ComposeClient) error { return c.FsClearDir("electrs", 1000, 1000, "0755") },
		},
		{
			// ComposeUpDetached also accepts 202; the 200 path is checked here,
			// the 202 path in its own test below.
			name: "ComposeUpDetached", wantPath: "/v1/compose/up-detached",
			wantBody: map[string]any{"service_id": "truffels-agent"},
			wantOp:   "agent compose up detached",
			call:     func(c *ComposeClient) error { return c.ComposeUpDetached("truffels-agent") },
		},
	}
}

func TestCharacterize_ErrorOnly_Success(t *testing.T) {
	for _, tc := range errorOnlyCases() {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newFakeAgent(t, 200, map[string]string{"status": "ok"})
			if err := tc.call(c); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rec.method != http.MethodPost {
				t.Errorf("method = %s, want POST", rec.method)
			}
			if rec.path != tc.wantPath {
				t.Errorf("path = %s, want %s", rec.path, tc.wantPath)
			}
			assertJSONBody(t, rec.body, tc.wantBody)
		})
	}
}

func TestCharacterize_ErrorOnly_AgentFailure(t *testing.T) {
	for _, tc := range errorOnlyCases() {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newFakeAgent(t, 500, failureBody())
			err := tc.call(c)
			assertContains(t, err, tc.wantOp+": ")
			assertContains(t, err, "exit status 1")
			// Every method in this group routes through agentError, so the
			// agent's own output has to survive into the message.
			assertContains(t, err, diagnosisMarker)
		})
	}
}

func TestCharacterize_ErrorOnly_TransportFailure(t *testing.T) {
	for _, tc := range errorOnlyCases() {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(unreachableAgent())
			assertContains(t, err, tc.wantOp+": ")
			// Transport errors are wrapped with %w, so callers can still reach
			// the *url.Error underneath.
			var urlErr *url.Error
			if !errors.As(err, &urlErr) {
				t.Errorf("transport error is not wrapped as *url.Error: %v", err)
			}
		})
	}
}

// A non-2xx response whose body is not JSON at all still yields an error — the
// decode failure is swallowed and only the status code decides.
func TestCharacterize_ErrorOnly_NonJSONFailureBody(t *testing.T) {
	c := newRawAgent(t, 502, "<html>bad gateway</html>")
	err := c.Up("bitcoind")
	assertContains(t, err, "agent /v1/compose/up: ")
}

// A 200 whose body is not JSON is still a success: the decode error is ignored
// and nothing about the response is inspected.
func TestCharacterize_ErrorOnly_NonJSONSuccessBody(t *testing.T) {
	c := newRawAgent(t, 200, "not json")
	if err := c.Up("bitcoind"); err != nil {
		t.Fatalf("expected success despite unparseable body, got %v", err)
	}
}

// --- optional-field payload shapes -----------------------------------------

// An empty oldTag omits the key entirely, which is what makes the agent's
// rewrite idempotent.
func TestCharacterize_RewriteTags_OmitsEmptyOldTag(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]string{"status": "ok"})
	if err := c.RewriteTags("mempool", []string{"a", "b"}, "", "v2"); err != nil {
		t.Fatalf("rewrite tags: %v", err)
	}
	assertJSONBody(t, rec.body, map[string]any{
		"service_id": "mempool",
		"images":     []any{"a", "b"},
		"new_tag":    "v2",
	})
	if strings.Contains(string(rec.body), "old_tag") {
		t.Errorf("old_tag must be absent when empty, body: %s", rec.body)
	}
}

// build_args and services are omitempty: with neither supplied only service_id
// goes on the wire.
func TestCharacterize_BuildWithArgs_OmitsEmptyOptionals(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]string{"status": "ok"})
	if err := c.BuildWithArgs("truffels-api", nil); err != nil {
		t.Fatalf("build with args: %v", err)
	}
	assertJSONBody(t, rec.body, map[string]any{"service_id": "truffels-api"})
}

// Build always sends service_id, even for an empty service.
func TestCharacterize_Build_EmptyServiceID(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]string{"status": "ok"})
	if err := c.Build(""); err != nil {
		t.Fatalf("build: %v", err)
	}
	assertJSONBody(t, rec.body, map[string]any{"service_id": ""})
}

// SystemAction builds its path from the action, so an unknown action simply
// produces an unknown path rather than being rejected client-side.
func TestCharacterize_SystemAction_PathFollowsAction(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]string{"status": "ok"})
	if err := c.SystemAction("shutdown"); err != nil {
		t.Fatalf("system action: %v", err)
	}
	if rec.path != "/v1/system/shutdown" {
		t.Errorf("path = %s, want /v1/system/shutdown", rec.path)
	}
	assertJSONBody(t, rec.body, map[string]any{"action": "shutdown"})
}

func TestCharacterize_SystemAction_ErrorPrefixCarriesAction(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	err := c.SystemAction("shutdown")
	assertContains(t, err, "agent system shutdown: ")
}

// --- ComposeUpDetached: 202 is a success ------------------------------------

// The detached self-update returns before the work is done, so 202 must not be
// treated as a failure.
func TestCharacterize_ComposeUpDetached_Accepts202(t *testing.T) {
	c, rec := newFakeAgent(t, 202, map[string]string{"status": "accepted"})
	if err := c.ComposeUpDetached("truffels-agent"); err != nil {
		t.Fatalf("202 must be accepted, got %v", err)
	}
	if rec.path != "/v1/compose/up-detached" {
		t.Errorf("path = %s", rec.path)
	}
}

// Only 200 and 202 pass; every other 2xx is an error.
func TestCharacterize_ComposeUpDetached_RejectsOther2xx(t *testing.T) {
	for _, status := range []int{201, 204} {
		c, _ := newFakeAgent(t, status, failureBody())
		err := c.ComposeUpDetached("truffels-agent")
		if err == nil {
			t.Errorf("status %d must be an error", status)
		}
	}
}

// No other method inherits the 202 tolerance.
func TestCharacterize_Up_Rejects202(t *testing.T) {
	c, _ := newFakeAgent(t, 202, failureBody())
	if err := c.Up("bitcoind"); err == nil {
		t.Fatal("202 must be an error for Up")
	}
}

// --- POST returning text from the agentResponse envelope --------------------

func TestCharacterize_Logs_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]string{"status": "ok", "logs": "line1\nline2\n"})
	logs, err := c.Logs("electrs", 250, "10m", "electrs-main")
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if logs != "line1\nline2\n" {
		t.Errorf("logs = %q", logs)
	}
	if rec.path != "/v1/compose/logs" || rec.method != http.MethodPost {
		t.Errorf("%s %s", rec.method, rec.path)
	}
	assertJSONBody(t, rec.body, map[string]any{
		"service_id": "electrs",
		"tail":       250,
		"since":      "10m",
		"container":  "electrs-main",
	})
}

// since and container are omitempty; tail is not.
func TestCharacterize_Logs_OmitsEmptyOptionals(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]string{"logs": ""})
	if _, err := c.Logs("electrs", 0, "", ""); err != nil {
		t.Fatalf("logs: %v", err)
	}
	assertJSONBody(t, rec.body, map[string]any{"service_id": "electrs", "tail": 0})
}

// On failure Logs returns BOTH the partial log text and the error. The UI shows
// whatever the agent managed to collect next to the failure.
func TestCharacterize_Logs_ReturnsPartialLogsWithError(t *testing.T) {
	c, _ := newFakeAgent(t, 500, map[string]string{
		"error":  "op failed: exit status 1",
		"output": diagnosisMarker,
		"logs":   "partial output",
	})
	logs, err := c.Logs("electrs", 100, "", "")
	assertContains(t, err, "agent logs: ")
	assertContains(t, err, diagnosisMarker)
	if logs != "partial output" {
		t.Errorf("partial logs must still be returned, got %q", logs)
	}
}

func TestCharacterize_Logs_TransportFailure(t *testing.T) {
	logs, err := unreachableAgent().Logs("electrs", 100, "", "")
	assertContains(t, err, "agent logs: ")
	if logs != "" {
		t.Errorf("logs = %q, want empty on transport failure", logs)
	}
}

func TestCharacterize_SystemJournal_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]string{"status": "ok", "logs": "journal text"})
	out, err := c.SystemJournal(500, "err", "docker.service", "2026-01-01", 2)
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	if out != "journal text" {
		t.Errorf("out = %q", out)
	}
	if rec.path != "/v1/system/journal" {
		t.Errorf("path = %s", rec.path)
	}
	// This payload is a plain map, so every key is present even when empty.
	assertJSONBody(t, rec.body, map[string]any{
		"lines": 500, "priority": "err", "unit": "docker.service",
		"since": "2026-01-01", "boot": 2,
	})
}

func TestCharacterize_SystemJournal_SendsEmptyKeys(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]string{"logs": ""})
	if _, err := c.SystemJournal(0, "", "", "", 0); err != nil {
		t.Fatalf("journal: %v", err)
	}
	assertJSONBody(t, rec.body, map[string]any{
		"lines": 0, "priority": "", "unit": "", "since": "", "boot": 0,
	})
}

// Like Logs, SystemJournal hands back the partial text with the error.
func TestCharacterize_SystemJournal_ReturnsPartialLogsWithError(t *testing.T) {
	c, _ := newFakeAgent(t, 500, map[string]string{
		"error": "op failed: exit status 1", "output": diagnosisMarker, "logs": "partial",
	})
	out, err := c.SystemJournal(100, "", "", "", 0)
	assertContains(t, err, "agent journal: ")
	assertContains(t, err, diagnosisMarker)
	if out != "partial" {
		t.Errorf("out = %q, want partial", out)
	}
}

func TestCharacterize_SystemJournal_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().SystemJournal(100, "", "", "", 0)
	assertContains(t, err, "agent journal: ")
}

func TestCharacterize_Pull_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]string{"status": "ok", "output": "pull log"})
	out, err := c.Pull("bitcoin/bitcoin:29.0")
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if out != "pull log" {
		t.Errorf("out = %q", out)
	}
	if rec.path != "/v1/image/pull" {
		t.Errorf("path = %s", rec.path)
	}
	assertJSONBody(t, rec.body, map[string]any{"image": "bitcoin/bitcoin:29.0"})
}

// Unlike Logs, Pull discards the output when the pull failed and returns "".
func TestCharacterize_Pull_DiscardsOutputOnError(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	out, err := c.Pull("mariadb:lts")
	assertContains(t, err, "agent pull: ")
	assertContains(t, err, diagnosisMarker)
	if out != "" {
		t.Errorf("out = %q, want empty on error", out)
	}
}

func TestCharacterize_Pull_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().Pull("img")
	assertContains(t, err, "agent pull: ")
}

// --- POST decoding into a typed struct --------------------------------------

func TestCharacterize_ImageInspect_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, ImageInfo{
		Image:  "bitcoin/bitcoin:29.0",
		Digest: "sha256:abc",
		Tags:   []string{"29.0"},
		Labels: map[string]string{"org.opencontainers.image.version": "29.0"},
	})
	info, err := c.ImageInspect("bitcoind")
	if err != nil {
		t.Fatalf("image inspect: %v", err)
	}
	if info.Image != "bitcoin/bitcoin:29.0" || info.Digest != "sha256:abc" {
		t.Errorf("info = %+v", info)
	}
	if len(info.Tags) != 1 || info.Tags[0] != "29.0" {
		t.Errorf("tags = %v", info.Tags)
	}
	if info.Labels["org.opencontainers.image.version"] != "29.0" {
		t.Errorf("labels = %v", info.Labels)
	}
	if rec.path != "/v1/image/inspect" {
		t.Errorf("path = %s", rec.path)
	}
	assertJSONBody(t, rec.body, map[string]any{"container": "bitcoind"})
}

func TestCharacterize_ImageInspect_AgentFailure(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	info, err := c.ImageInspect("bitcoind")
	assertContains(t, err, "agent image inspect: ")
	assertContains(t, err, diagnosisMarker)
	if info != nil {
		t.Errorf("info = %+v, want nil", info)
	}
}

// A 200 with an unparseable body is reported separately from an agent failure,
// with its own "decode" suffix.
func TestCharacterize_ImageInspect_DecodeFailure(t *testing.T) {
	c := newRawAgent(t, 200, "not json")
	_, err := c.ImageInspect("bitcoind")
	assertContains(t, err, "agent image inspect decode: ")
}

func TestCharacterize_ImageInspect_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().ImageInspect("bitcoind")
	assertContains(t, err, "agent image inspect: ")
}

func TestCharacterize_ImageInspectByName_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, ImageInfo{Image: "truffels-api:v0.3.0", Digest: "sha256:def"})
	info, err := c.ImageInspectByName("truffels-api:v0.3.0")
	if err != nil {
		t.Fatalf("inspect by name: %v", err)
	}
	if info.Digest != "sha256:def" {
		t.Errorf("info = %+v", info)
	}
	if rec.path != "/v1/image/inspect-by-name" {
		t.Errorf("path = %s", rec.path)
	}
	assertJSONBody(t, rec.body, map[string]any{"image": "truffels-api:v0.3.0"})
}

// Its error prefix differs from ImageInspect's — "by name" is part of the text.
func TestCharacterize_ImageInspectByName_AgentFailure(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	_, err := c.ImageInspectByName("img")
	assertContains(t, err, "agent image inspect by name: ")
	assertContains(t, err, diagnosisMarker)
}

func TestCharacterize_ImageInspectByName_DecodeFailure(t *testing.T) {
	c := newRawAgent(t, 200, "{{{")
	_, err := c.ImageInspectByName("img")
	assertContains(t, err, "agent image inspect by name decode: ")
}

func TestCharacterize_ImageInspectByName_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().ImageInspectByName("img")
	assertContains(t, err, "agent image inspect by name: ")
}

// --- GET endpoints ----------------------------------------------------------

func TestCharacterize_SystemInfoGet_Success(t *testing.T) {
	want := SystemInfo{
		Hostname: "truffels", OS: "Debian", Kernel: "6.12", Model: "Raspberry Pi 5",
		CPUCores: 4, MemTotal: "8G", MemFree: "2G", Uptime: "3d",
		Networks: []NetworkIfInfo{{Name: "wlan0", IP: "192.0.2.10", MAC: "aa:bb"}},
		Storage:  []StorageInfo{{Device: "/dev/nvme0n1p2", Mount: "/", UsePct: "53%"}},
		DockerStorage: []DockerStorageItem{
			{Type: "Images", Count: 13, TotalSize: "8GB", Reclaimable: "2GB", ReclaimableRaw: 2147483648},
		},
		ServiceData: []ServiceDataItem{{Path: "/srv/truffels/data/bitcoin", Size: "700G", SizeRaw: 751619276800}},
	}
	c, rec := newFakeAgent(t, 200, want)
	got, err := c.SystemInfoGet()
	if err != nil {
		t.Fatalf("system info: %v", err)
	}
	if got.Hostname != "truffels" || got.CPUCores != 4 {
		t.Errorf("got = %+v", got)
	}
	if len(got.Networks) != 1 || got.Networks[0].IP != "192.0.2.10" {
		t.Errorf("networks = %+v", got.Networks)
	}
	if len(got.DockerStorage) != 1 || got.DockerStorage[0].ReclaimableRaw != 2147483648 {
		t.Errorf("docker storage = %+v", got.DockerStorage)
	}
	if len(got.ServiceData) != 1 || got.ServiceData[0].SizeRaw != 751619276800 {
		t.Errorf("service data = %+v", got.ServiceData)
	}
	// This one is a GET with no request body.
	if rec.method != http.MethodGet {
		t.Errorf("method = %s, want GET", rec.method)
	}
	if rec.path != "/v1/system/info" {
		t.Errorf("path = %s", rec.path)
	}
	if len(rec.body) != 0 {
		t.Errorf("GET must not carry a body, got %q", rec.body)
	}
}

// SystemInfoGet never looks at the status code. A 500 carrying a decodable body
// is reported as success. Preserved deliberately — changing it would alter what
// the system tab shows when the agent is degraded.
func TestCharacterize_SystemInfoGet_IgnoresStatusCode(t *testing.T) {
	c, _ := newFakeAgent(t, 500, SystemInfo{Hostname: "degraded"})
	got, err := c.SystemInfoGet()
	if err != nil {
		t.Fatalf("status code is not checked today, want nil error, got %v", err)
	}
	if got.Hostname != "degraded" {
		t.Errorf("hostname = %q", got.Hostname)
	}
}

func TestCharacterize_SystemInfoGet_DecodeFailure(t *testing.T) {
	c := newRawAgent(t, 200, "nope")
	_, err := c.SystemInfoGet()
	assertContains(t, err, "agent system info decode: ")
}

func TestCharacterize_SystemInfoGet_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().SystemInfoGet()
	assertContains(t, err, "agent system info: ")
}

func TestCharacterize_HostDirSize_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]any{"size_bytes": 751619276800, "fresh": true})
	size, fresh, err := c.HostDirSize("/srv/truffels/data/bitcoin")
	if err != nil {
		t.Fatalf("dir size: %v", err)
	}
	if size != 751619276800 || !fresh {
		t.Errorf("size = %d, fresh = %v", size, fresh)
	}
	if rec.method != http.MethodGet || rec.path != "/v1/host/dir-size" {
		t.Errorf("%s %s", rec.method, rec.path)
	}
	if rec.rawQuery != "path=%2Fsrv%2Ftruffels%2Fdata%2Fbitcoin" {
		t.Errorf("query = %q", rec.rawQuery)
	}
}

// The path is query-escaped, so spaces and separators survive intact.
func TestCharacterize_HostDirSize_EscapesPath(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]any{"size_bytes": 0, "fresh": false})
	if _, _, err := c.HostDirSize("/srv/a b&c=d"); err != nil {
		t.Fatalf("dir size: %v", err)
	}
	if rec.rawQuery != "path=%2Fsrv%2Fa+b%26c%3Dd" {
		t.Errorf("query = %q", rec.rawQuery)
	}
}

// A path the agent has not walked yet comes back as (-1, false, nil).
func TestCharacterize_HostDirSize_NotYetWalked(t *testing.T) {
	c, _ := newFakeAgent(t, 200, map[string]any{"size_bytes": -1, "fresh": false})
	size, fresh, err := c.HostDirSize("/srv/truffels/data/electrs")
	if err != nil {
		t.Fatalf("dir size: %v", err)
	}
	if size != -1 || fresh {
		t.Errorf("size = %d, fresh = %v, want -1/false", size, fresh)
	}
}

// Like SystemInfoGet, the status code is not consulted.
func TestCharacterize_HostDirSize_IgnoresStatusCode(t *testing.T) {
	c, _ := newFakeAgent(t, 500, map[string]any{"size_bytes": 42, "fresh": true})
	size, fresh, err := c.HostDirSize("/srv/x")
	if err != nil {
		t.Fatalf("status code is not checked today, want nil error, got %v", err)
	}
	if size != 42 || !fresh {
		t.Errorf("size = %d, fresh = %v", size, fresh)
	}
}

func TestCharacterize_HostDirSize_DecodeFailure(t *testing.T) {
	c := newRawAgent(t, 200, "nope")
	_, _, err := c.HostDirSize("/srv/x")
	assertContains(t, err, "agent dir-size decode: ")
}

func TestCharacterize_HostDirSize_TransportFailure(t *testing.T) {
	_, _, err := unreachableAgent().HostDirSize("/srv/x")
	assertContains(t, err, "agent dir-size: ")
}

func TestCharacterize_SystemTuningGet_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, SystemTuningInfo{
		PersistentJournal: true,
		Swappiness:        10,
		JournalDiskUsage:  "112M",
		Boots:             []BootEntry{{Index: 0, ID: "abc", First: "t0", Last: "t1"}},
	})
	got, err := c.SystemTuningGet()
	if err != nil {
		t.Fatalf("tuning get: %v", err)
	}
	if !got.PersistentJournal || got.Swappiness != 10 || got.JournalDiskUsage != "112M" {
		t.Errorf("got = %+v", got)
	}
	if len(got.Boots) != 1 || got.Boots[0].ID != "abc" {
		t.Errorf("boots = %+v", got.Boots)
	}
	if rec.method != http.MethodGet || rec.path != "/v1/system/tuning" {
		t.Errorf("%s %s", rec.method, rec.path)
	}
}

// Unlike the other two GETs, tuning DOES check the status code and routes
// through agentError.
func TestCharacterize_SystemTuningGet_AgentFailure(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	got, err := c.SystemTuningGet()
	assertContains(t, err, "agent tuning get: ")
	assertContains(t, err, diagnosisMarker)
	if got != nil {
		t.Errorf("got = %+v, want nil", got)
	}
}

func TestCharacterize_SystemTuningGet_DecodeFailure(t *testing.T) {
	c := newRawAgent(t, 200, "nope")
	_, err := c.SystemTuningGet()
	assertContains(t, err, "agent tuning decode: ")
}

func TestCharacterize_SystemTuningGet_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().SystemTuningGet()
	assertContains(t, err, "agent tuning get: ")
}

// --- the reconcile / prune / read family ------------------------------------
//
// These do NOT use agentError: they report ar.Error only and drop the agent's
// output. Asserting the absence of the marker is intentional — it is what keeps
// a refactor from quietly folding them into the agentError path and changing
// every one of these messages.

func TestCharacterize_FileReconcile_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]any{"status": "ok", "changed": true})
	changed, err := c.FileReconcile("/srv/truffels/config/Caddyfile", "content")
	if err != nil {
		t.Fatalf("file reconcile: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if rec.path != "/v1/file/reconcile" || rec.method != http.MethodPost {
		t.Errorf("%s %s", rec.method, rec.path)
	}
	assertJSONBody(t, rec.body, map[string]any{
		"path": "/srv/truffels/config/Caddyfile", "expected_content": "content",
	})
}

func TestCharacterize_FileReconcile_Unchanged(t *testing.T) {
	c, _ := newFakeAgent(t, 200, map[string]any{"status": "ok", "changed": false})
	changed, err := c.FileReconcile("/p", "c")
	if err != nil {
		t.Fatalf("file reconcile: %v", err)
	}
	if changed {
		t.Error("changed = true, want false")
	}
}

func TestCharacterize_FileReconcile_AgentFailure(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	changed, err := c.FileReconcile("/p", "c")
	assertContains(t, err, "agent file reconcile: op failed: exit status 1")
	if strings.Contains(err.Error(), diagnosisMarker) {
		t.Errorf("this method does not use agentError; output must not appear: %v", err)
	}
	if changed {
		t.Error("changed must be false on error")
	}
}

func TestCharacterize_FileReconcile_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().FileReconcile("/p", "c")
	assertContains(t, err, "agent file reconcile: ")
}

// ReconcileFile hits the same endpoint with the same payload keys and the same
// error text as FileReconcile; only the parameter name differs.
func TestCharacterize_ReconcileFile_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]any{"status": "ok", "changed": true})
	changed, err := c.ReconcileFile("ckstats/Dockerfile", "FROM node")
	if err != nil {
		t.Fatalf("reconcile file: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if rec.path != "/v1/file/reconcile" {
		t.Errorf("path = %s", rec.path)
	}
	assertJSONBody(t, rec.body, map[string]any{
		"path": "ckstats/Dockerfile", "expected_content": "FROM node",
	})
}

func TestCharacterize_ReconcileFile_AgentFailure(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	_, err := c.ReconcileFile("ckstats/Dockerfile", "x")
	assertContains(t, err, "agent file reconcile: op failed: exit status 1")
	if strings.Contains(err.Error(), diagnosisMarker) {
		t.Errorf("output must not appear: %v", err)
	}
}

func TestCharacterize_ReconcileFile_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().ReconcileFile("p", "c")
	assertContains(t, err, "agent file reconcile: ")
}

func TestCharacterize_ComposeReconcile_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]any{"status": "ok", "changed": true})
	changed, err := c.ComposeReconcile("mempool", "services: {}")
	if err != nil {
		t.Fatalf("compose reconcile: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}
	if rec.path != "/v1/compose/reconcile" {
		t.Errorf("path = %s", rec.path)
	}
	assertJSONBody(t, rec.body, map[string]any{
		"service_id": "mempool", "expected_content": "services: {}",
	})
}

func TestCharacterize_ComposeReconcile_AgentFailure(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	changed, err := c.ComposeReconcile("mempool", "x")
	assertContains(t, err, "agent compose reconcile: op failed: exit status 1")
	if strings.Contains(err.Error(), diagnosisMarker) {
		t.Errorf("output must not appear: %v", err)
	}
	if changed {
		t.Error("changed must be false on error")
	}
}

func TestCharacterize_ComposeReconcile_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().ComposeReconcile("mempool", "x")
	assertContains(t, err, "agent compose reconcile: ")
}

func TestCharacterize_ComposeRead_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]any{"status": "ok", "content": "services:\n  a: {}\n"})
	content, err := c.ComposeRead("mempool")
	if err != nil {
		t.Fatalf("compose read: %v", err)
	}
	if content != "services:\n  a: {}\n" {
		t.Errorf("content = %q", content)
	}
	if rec.path != "/v1/compose/read" {
		t.Errorf("path = %s", rec.path)
	}
	assertJSONBody(t, rec.body, map[string]any{"service_id": "mempool"})
}

func TestCharacterize_ComposeRead_AgentFailure(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	content, err := c.ComposeRead("mempool")
	assertContains(t, err, "agent compose read: op failed: exit status 1")
	if strings.Contains(err.Error(), diagnosisMarker) {
		t.Errorf("output must not appear: %v", err)
	}
	if content != "" {
		t.Errorf("content = %q, want empty", content)
	}
}

func TestCharacterize_ComposeRead_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().ComposeRead("mempool")
	assertContains(t, err, "agent compose read: ")
}

func TestCharacterize_DockerPrune_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]any{"status": "ok", "reclaimed": "4.2GB"})
	reclaimed, err := c.DockerPrune()
	if err != nil {
		t.Fatalf("docker prune: %v", err)
	}
	if reclaimed != "4.2GB" {
		t.Errorf("reclaimed = %q", reclaimed)
	}
	if rec.path != "/v1/docker/prune" || rec.method != http.MethodPost {
		t.Errorf("%s %s", rec.method, rec.path)
	}
	// An empty JSON object, not an empty body.
	if strings.TrimSpace(string(rec.body)) != "{}" {
		t.Errorf("body = %q, want {}", rec.body)
	}
}

func TestCharacterize_DockerPrune_AgentFailure(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	reclaimed, err := c.DockerPrune()
	assertContains(t, err, "agent docker prune: op failed: exit status 1")
	if strings.Contains(err.Error(), diagnosisMarker) {
		t.Errorf("output must not appear: %v", err)
	}
	if reclaimed != "" {
		t.Errorf("reclaimed = %q, want empty", reclaimed)
	}
}

func TestCharacterize_DockerPrune_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().DockerPrune()
	assertContains(t, err, "agent docker prune: ")
}

func TestCharacterize_DockerPruneBuildCache_Success(t *testing.T) {
	c, rec := newFakeAgent(t, 200, map[string]any{"status": "ok", "reclaimed": "1.1GB"})
	reclaimed, err := c.DockerPruneBuildCache()
	if err != nil {
		t.Fatalf("prune buildcache: %v", err)
	}
	if reclaimed != "1.1GB" {
		t.Errorf("reclaimed = %q", reclaimed)
	}
	if rec.path != "/v1/docker/prune-buildcache" {
		t.Errorf("path = %s", rec.path)
	}
	if strings.TrimSpace(string(rec.body)) != "{}" {
		t.Errorf("body = %q, want {}", rec.body)
	}
}

// Its error prefix carries "buildcache" and is distinct from DockerPrune's.
func TestCharacterize_DockerPruneBuildCache_AgentFailure(t *testing.T) {
	c, _ := newFakeAgent(t, 500, failureBody())
	_, err := c.DockerPruneBuildCache()
	assertContains(t, err, "agent docker prune buildcache: op failed: exit status 1")
	if strings.Contains(err.Error(), diagnosisMarker) {
		t.Errorf("output must not appear: %v", err)
	}
}

func TestCharacterize_DockerPruneBuildCache_TransportFailure(t *testing.T) {
	_, err := unreachableAgent().DockerPruneBuildCache()
	assertContains(t, err, "agent docker prune buildcache: ")
}

// --- agentError itself ------------------------------------------------------

func TestCharacterize_AgentError_KeepsOutputTail(t *testing.T) {
	long := strings.Repeat("x", 4000) + "THE-ACTUAL-CAUSE"
	err := agentError("agent op", agentResponse{Error: "boom", Output: long})
	assertContains(t, err, "agent op: boom")
	assertContains(t, err, "THE-ACTUAL-CAUSE")
	// 500 chars of tail plus the ellipsis, the prefix and the error line.
	if len(err.Error()) > 700 {
		t.Errorf("output was not truncated: %d chars", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "...") {
		t.Errorf("truncation marker missing: %v", err)
	}
}

func TestCharacterize_AgentError_NoOutput(t *testing.T) {
	err := agentError("agent op", agentResponse{Error: "boom"})
	if err.Error() != "agent op: boom" {
		t.Errorf("err = %q, want %q", err, "agent op: boom")
	}
}

// Short output is appended whole, on its own line, with no ellipsis.
func TestCharacterize_AgentError_ShortOutputNotTruncated(t *testing.T) {
	err := agentError("agent op", agentResponse{Error: "boom", Output: "short cause"})
	if err.Error() != "agent op: boom\nshort cause" {
		t.Errorf("err = %q", err)
	}
}

// An empty Error field with output still produces a message carrying the cause.
func TestCharacterize_AgentError_EmptyErrorField(t *testing.T) {
	err := agentError("agent op", agentResponse{Output: "only output"})
	if err.Error() != "agent op: \nonly output" {
		t.Errorf("err = %q", err)
	}
}
