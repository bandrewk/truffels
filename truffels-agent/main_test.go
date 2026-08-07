package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Health ---

func TestHandleHealth(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/health", nil)
	handleHealth(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("expected ok, got %q", body["status"])
	}
}

// --- composeDir ---

func TestComposeDir(t *testing.T) {
	composeRoot = "/srv/truffels/compose"

	tests := []struct {
		serviceID string
		want      string
	}{
		{"bitcoind", "/srv/truffels/compose/bitcoin"},
		{"electrs", "/srv/truffels/compose/electrs"},
		{"truffels-api", "/srv/truffels/compose/truffels"},
		{"truffels-web", "/srv/truffels/compose/truffels"},
		{"proxy", "/srv/truffels/compose/proxy"},
	}

	for _, tt := range tests {
		got := composeDir(tt.serviceID)
		if got != tt.want {
			t.Fatalf("composeDir(%q) = %q, want %q", tt.serviceID, got, tt.want)
		}
	}
}

// --- decodeAndValidate ---

func TestDecodeAndValidate_ValidService(t *testing.T) {
	body, _ := json.Marshal(serviceRequest{ServiceID: "bitcoind"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/up", bytes.NewReader(body))

	var req serviceRequest
	ok := decodeAndValidate(w, r, &req)
	if !ok {
		t.Fatal("expected valid")
	}
	if req.ServiceID != "bitcoind" {
		t.Fatalf("expected bitcoind, got %q", req.ServiceID)
	}
}

func TestDecodeAndValidate_InvalidService(t *testing.T) {
	body, _ := json.Marshal(serviceRequest{ServiceID: "hacker"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/up", bytes.NewReader(body))

	var req serviceRequest
	ok := decodeAndValidate(w, r, &req)
	if ok {
		t.Fatal("expected rejected for disallowed service")
	}
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestDecodeAndValidate_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/up", bytes.NewReader([]byte("not json")))

	var req serviceRequest
	ok := decodeAndValidate(w, r, &req)
	if ok {
		t.Fatal("expected rejected for bad JSON")
	}
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Allowlists ---

func TestAllowedServices(t *testing.T) {
	expected := []string{
		"bitcoind", "electrs", "ckpool", "mempool", "ckstats",
		"proxy", "mempool-db", "ckstats-db",
		"truffels", "truffels-agent", "truffels-api", "truffels-web",
	}
	for _, id := range expected {
		if _, ok := allowedServices[id]; !ok {
			t.Fatalf("expected %q in allowedServices", id)
		}
	}
}

func TestAllowedContainers(t *testing.T) {
	expected := []string{
		"truffels-bitcoind", "truffels-electrs", "truffels-ckpool",
		"truffels-mempool-backend", "truffels-mempool-frontend", "truffels-mempool-db",
		"truffels-ckstats", "truffels-ckstats-cron", "truffels-ckstats-db",
		"truffels-proxy", "truffels-agent", "truffels-api", "truffels-web",
	}
	for _, name := range expected {
		if !allowedContainers[name] {
			t.Fatalf("expected %q in allowedContainers", name)
		}
	}
}

func TestAllowedContainers_Denied(t *testing.T) {
	denied := []string{"postgres", "redis", "nginx", "random-container"}
	for _, name := range denied {
		if allowedContainers[name] {
			t.Fatalf("%q should not be in allowedContainers", name)
		}
	}
}

// --- handleInspect ---

func TestHandleInspect_DeniedContainer(t *testing.T) {
	body, _ := json.Marshal(inspectRequest{Containers: []string{"unauthorized-container"}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/inspect", bytes.NewReader(body))

	handleInspect(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200 (partial result), got %d", w.Code)
	}

	var states []containerState
	_ = json.Unmarshal(w.Body.Bytes(), &states)
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}
	if states[0].Status != "denied" {
		t.Fatalf("expected denied, got %q", states[0].Status)
	}
}

func TestHandleInspect_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/inspect", bytes.NewReader([]byte("{")))

	handleInspect(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- handleComposeLogs validation ---

func TestHandleComposeLogs_InvalidService(t *testing.T) {
	body, _ := json.Marshal(logsRequest{ServiceID: "hacker", Tail: 100})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/logs", bytes.NewReader(body))

	handleComposeLogs(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeLogs_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/logs", bytes.NewReader([]byte("bad")))

	handleComposeLogs(w, r)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- writeJSON ---

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 201, map[string]string{"key": "value"})

	if w.Code != 201 {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["key"] != "value" {
		t.Fatalf("expected value, got %q", body["key"])
	}
}

// --- Stats Parsing ---

func TestParsePercent(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"65.71%", 65.71},
		{"0.00%", 0},
		{"100.00%", 100},
		{"  3.5% ", 3.5},
		{"", 0},
	}
	for _, tt := range tests {
		got := parsePercent(tt.input)
		if got != tt.want {
			t.Errorf("parsePercent(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseBytes(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"909MB", 909e6},
		{"30.7GB", 30.7e9},
		{"7.09kB", 7090},
		{"2.083GiB", 2.083 * 1024 * 1024 * 1024},
		{"64.55MiB", 64.55 * 1024 * 1024},
		{"0B", 0},
		{"1TB", 1e12},
		{"512KiB", 512 * 1024},
	}
	for _, tt := range tests {
		got := parseBytes(tt.input)
		// Allow 0.1% tolerance for floating point
		diff := got - tt.want
		if diff < 0 {
			diff = -diff
		}
		if tt.want != 0 && diff/tt.want > 0.001 {
			t.Errorf("parseBytes(%q) = %v, want %v", tt.input, got, tt.want)
		} else if tt.want == 0 && got != 0 {
			t.Errorf("parseBytes(%q) = %v, want 0", tt.input, got)
		}
	}
}

func TestParseMemUsage(t *testing.T) {
	usage, limit := parseMemUsage("2.083GiB / 3.418GiB")
	if usage < 2130 || usage > 2140 {
		t.Errorf("expected ~2133 MB usage, got %.1f", usage)
	}
	if limit < 3500 || limit > 3510 {
		t.Errorf("expected ~3501 MB limit, got %.1f", limit)
	}
}

func TestParseNetIO(t *testing.T) {
	rx, tx := parseNetIO("909MB / 30.7GB")
	if rx != 909000000 {
		t.Errorf("expected rx=909000000, got %d", rx)
	}
	if tx != 30700000000 {
		t.Errorf("expected tx=30700000000, got %d", tx)
	}
}

func TestParseMemUsage_Empty(t *testing.T) {
	usage, limit := parseMemUsage("")
	if usage != 0 || limit != 0 {
		t.Errorf("expected 0/0, got %.1f/%.1f", usage, limit)
	}
}

func TestParseNetIO_Empty(t *testing.T) {
	rx, tx := parseNetIO("")
	if rx != 0 || tx != 0 {
		t.Errorf("expected 0/0, got %d/%d", rx, tx)
	}
}

// --- envOr ---

func TestEnvOr(t *testing.T) {
	got := envOr("TRUFFELS_TEST_NONEXISTENT_12345", "fallback")
	if got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}

	t.Setenv("TRUFFELS_TEST_VAR_12345", "custom")
	got = envOr("TRUFFELS_TEST_VAR_12345", "fallback")
	if got != "custom" {
		t.Fatalf("expected custom, got %q", got)
	}
}

// --- handleSystemRestart / handleSystemShutdown ---

func TestHandleSystemShutdown_ReturnsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/shutdown", nil)

	handleSystemShutdown(w, r)

	// nsenter will fail in test environment, expect 500
	if w.Code != 500 {
		// If it somehow returns 200, that's fine too (means nsenter succeeded)
		if w.Code != 200 {
			t.Fatalf("expected 500 or 200, got %d", w.Code)
		}
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["status"] == "" {
		t.Fatal("expected 'status' field in response")
	}
}

func TestHandleSystemRestart_ReturnsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/restart", nil)

	handleSystemRestart(w, r)

	// nsenter will fail in test environment, expect 500
	if w.Code != 500 {
		if w.Code != 200 {
			t.Fatalf("expected 500 or 200, got %d", w.Code)
		}
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["status"] == "" {
		t.Fatal("expected 'status' field in response")
	}
}

// --- handleComposeStop ---

func TestHandleComposeStop_InvalidService(t *testing.T) {
	body, _ := json.Marshal(serviceRequest{ServiceID: "hacker"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/stop", bytes.NewReader(body))

	handleComposeStop(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeStop_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/stop", bytes.NewReader([]byte("{bad")))

	handleComposeStop(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- handleComposeUp ---

func TestHandleComposeUp_InvalidService(t *testing.T) {
	body, _ := json.Marshal(serviceRequest{ServiceID: "malicious"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/up", bytes.NewReader(body))

	handleComposeUp(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeUp_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/up", bytes.NewReader([]byte("nope")))

	handleComposeUp(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- handleComposeDown ---

func TestHandleComposeDown_InvalidService(t *testing.T) {
	body, _ := json.Marshal(serviceRequest{ServiceID: "evil"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/down", bytes.NewReader(body))

	handleComposeDown(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeDown_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/down", bytes.NewReader([]byte("[")))

	handleComposeDown(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- handleComposeRestart ---

func TestHandleComposeRestart_InvalidService(t *testing.T) {
	body, _ := json.Marshal(serviceRequest{ServiceID: "unknown"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/restart", bytes.NewReader(body))

	handleComposeRestart(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeRestart_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/restart", bytes.NewReader([]byte("}{}")))

	handleComposeRestart(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- handleImagePull ---

func TestHandleImagePull_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/pull", bytes.NewReader([]byte("not-json")))

	handleImagePull(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "invalid request" {
		t.Fatalf("expected 'invalid request' error, got %q", body["error"])
	}
}

func TestHandleImagePull_EmptyImage(t *testing.T) {
	reqBody, _ := json.Marshal(imagePullRequest{Image: ""})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/pull", bytes.NewReader(reqBody))

	handleImagePull(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "image required" {
		t.Fatalf("expected 'image required' error, got %q", body["error"])
	}
}

// --- handleImageInspect ---

func TestHandleImageInspect_DeniedContainer(t *testing.T) {
	reqBody, _ := json.Marshal(imageInspectRequest{Container: "not-allowed"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/inspect", bytes.NewReader(reqBody))

	handleImageInspect(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "container not allowed" {
		t.Fatalf("expected 'container not allowed' error, got %q", body["error"])
	}
}

func TestHandleImageInspect_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/inspect", bytes.NewReader([]byte("{bad}")))

	handleImageInspect(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- handleStats ---

func TestHandleStats_ReturnsJSONArray(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/stats", nil)

	handleStats(w, r)

	// docker stats will fail in test environment (no docker), expect 500
	// But if docker is available, expect 200 with JSON array
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	if w.Code == 200 {
		var stats []containerStats
		if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
			t.Fatalf("expected valid JSON array, got error: %v", err)
		}
	} else if w.Code == 500 {
		// Expected when docker is not available — verify error is valid JSON
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("expected valid JSON error, got: %v", err)
		}
		if body["error"] == "" {
			t.Fatal("expected 'error' field in 500 response")
		}
	} else {
		t.Fatalf("expected 200 or 500, got %d", w.Code)
	}
}

// --- handleComposeBuild ---

func TestHandleComposeBuild_InvalidService(t *testing.T) {
	body, _ := json.Marshal(serviceRequest{ServiceID: "rogue"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/build", bytes.NewReader(body))

	handleComposeBuild(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeBuild_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/build", bytes.NewReader([]byte("garbage")))

	handleComposeBuild(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- handleSystemJournal ---

func TestHandleSystemJournal_InvalidPriority(t *testing.T) {
	body, _ := json.Marshal(journalRequest{Lines: 100, Priority: "invalid"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/journal", bytes.NewReader(body))

	handleSystemJournal(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "invalid priority" {
		t.Fatalf("expected 'invalid priority', got %q", resp["error"])
	}
}

func TestHandleSystemJournal_InvalidUnit(t *testing.T) {
	body, _ := json.Marshal(journalRequest{Lines: 100, Unit: "mysql"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/journal", bytes.NewReader(body))

	handleSystemJournal(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "invalid unit" {
		t.Fatalf("expected 'invalid unit', got %q", resp["error"])
	}
}

func TestHandleSystemJournal_InvalidBoot(t *testing.T) {
	body, _ := json.Marshal(journalRequest{Lines: 100, Boot: 1})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/journal", bytes.NewReader(body))

	handleSystemJournal(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "boot must be 0 or negative" {
		t.Fatalf("expected 'boot must be 0 or negative', got %q", resp["error"])
	}
}

func TestHandleSystemJournal_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/journal", bytes.NewReader([]byte("bad")))

	handleSystemJournal(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleSystemJournal_ValidRequest(t *testing.T) {
	body, _ := json.Marshal(journalRequest{Lines: 50, Priority: "err", Unit: "docker", Boot: 0})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/journal", bytes.NewReader(body))

	handleSystemJournal(w, r)

	// nsenter will fail in CI, expect 500
	if w.Code != 500 && w.Code != 200 {
		t.Fatalf("expected 500 or 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
}

func TestHandleSystemJournal_ValidPriorities(t *testing.T) {
	for _, p := range []string{"", "emerg", "crit", "err", "warning", "info", "debug"} {
		body, _ := json.Marshal(journalRequest{Lines: 10, Priority: p})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/system/journal", bytes.NewReader(body))
		handleSystemJournal(w, r)
		// Should not be 400
		if w.Code == 400 {
			t.Fatalf("priority %q should be valid, got 400", p)
		}
	}
}

func TestHandleSystemJournal_ValidUnits(t *testing.T) {
	for _, u := range []string{"", "docker", "kernel", "systemd", "nftables", "ssh"} {
		body, _ := json.Marshal(journalRequest{Lines: 10, Unit: u})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/system/journal", bytes.NewReader(body))
		handleSystemJournal(w, r)
		if w.Code == 400 {
			t.Fatalf("unit %q should be valid, got 400", u)
		}
	}
}

func TestHandleSystemJournal_LinesClamp(t *testing.T) {
	// Lines 0 should be clamped to 200, not rejected
	body, _ := json.Marshal(journalRequest{Lines: 0})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/journal", bytes.NewReader(body))
	handleSystemJournal(w, r)
	// Should not be 400 (lines gets clamped)
	if w.Code == 400 {
		t.Fatal("lines=0 should be clamped, not rejected")
	}
}

// --- handleSystemTuningGet ---

// --- handleSystemInfo ---

func TestHandleSystemInfo_ReturnsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/system/info", nil)

	handleSystemInfo(w, r)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	var resp systemInfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	// cpu_cores should be 0 in CI (nsenter fails), but struct should decode
	if resp.CPUCores < 0 {
		t.Fatal("cpu_cores should not be negative")
	}
}

// dev.19: when a service data dir has no child subdirectories (only files,
// e.g. truffels/truffels.db) OR has only one child (e.g. ckpool/logs),
// the top-level service path must still be emitted as a row so 1-level
// template paths (ckpool, truffels) get a hit instead of "—" in the UI.
func TestHandleSystemInfo_EmitsTopLevelServiceDataPath(t *testing.T) {
	tmpRoot := t.TempDir()
	// Set up data dirs that mirror real-world cases:
	//   - "ckpool" with a single child dir "logs"
	//   - "truffels" with only a file (no child dirs)
	//   - "bitcoin" with a child dir "blockchain" (2-level template path)
	if err := os.MkdirAll(filepath.Join(tmpRoot, "ckpool", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpRoot, "truffels"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpRoot, "truffels", "truffels.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpRoot, "bitcoin", "blockchain"), 0o755); err != nil {
		t.Fatal(err)
	}

	prevRoot := dataRoot
	prevCache := sizeCache
	dataRoot = tmpRoot
	sizeCache = newDirSizeCache()
	// Seed cache so paths return real sizes instead of "calculating..."
	for _, p := range []string{
		filepath.Join(tmpRoot, "ckpool"),
		filepath.Join(tmpRoot, "ckpool", "logs"),
		filepath.Join(tmpRoot, "truffels"),
		filepath.Join(tmpRoot, "bitcoin"),
		filepath.Join(tmpRoot, "bitcoin", "blockchain"),
	} {
		sizeCache.set(p, 1024)
	}
	defer func() {
		dataRoot = prevRoot
		sizeCache = prevCache
	}()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/system/info", nil)
	handleSystemInfo(w, r)

	var resp systemInfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	gotPaths := make(map[string]bool)
	for _, sd := range resp.ServiceData {
		gotPaths[sd.Path] = true
	}
	mustHave := []string{
		filepath.Join(tmpRoot, "ckpool"),              // 1-level template path
		filepath.Join(tmpRoot, "truffels"),            // 1-level + no child dirs
		filepath.Join(tmpRoot, "bitcoin", "blockchain"), // 2-level template path
	}
	for _, p := range mustHave {
		if !gotPaths[p] {
			t.Errorf("expected service_data to include %q, got paths: %v", p, mapKeys(gotPaths))
		}
	}
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- handleSystemTuningGet ---

func TestHandleSystemTuningGet_ReturnsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/system/tuning", nil)

	handleSystemTuningGet(w, r)

	// nsenter will fail in CI but response should still be JSON
	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	var resp tuningResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

// --- handleSystemTuningSet ---

func TestHandleSystemTuningSet_UnknownAction(t *testing.T) {
	body, _ := json.Marshal(tuningSetRequest{Action: "reboot", Value: "now"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/tuning", bytes.NewReader(body))

	handleSystemTuningSet(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "unknown action" {
		t.Fatalf("expected 'unknown action', got %q", resp["error"])
	}
}

func TestHandleSystemTuningSet_InvalidJournalValue(t *testing.T) {
	body, _ := json.Marshal(tuningSetRequest{Action: "set_persistent_journal", Value: "maybe"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/tuning", bytes.NewReader(body))

	handleSystemTuningSet(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "value must be true or false" {
		t.Fatalf("expected 'value must be true or false', got %q", resp["error"])
	}
}

func TestHandleSystemTuningSet_InvalidSwappiness(t *testing.T) {
	tests := []struct {
		value string
	}{
		{"-1"},
		{"101"},
		{"abc"},
	}
	for _, tt := range tests {
		body, _ := json.Marshal(tuningSetRequest{Action: "set_swappiness", Value: tt.value})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/system/tuning", bytes.NewReader(body))

		handleSystemTuningSet(w, r)

		if w.Code != 400 {
			t.Fatalf("swappiness=%q: expected 400, got %d", tt.value, w.Code)
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["error"] != "swappiness must be 0-100" {
			t.Fatalf("swappiness=%q: expected 'swappiness must be 0-100', got %q", tt.value, resp["error"])
		}
	}
}

func TestHandleSystemTuningSet_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/tuning", bytes.NewReader([]byte("nope")))

	handleSystemTuningSet(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleSystemTuningSet_ValidSwappiness(t *testing.T) {
	// Valid request — will fail at nsenter in CI, but should not be 400
	body, _ := json.Marshal(tuningSetRequest{Action: "set_swappiness", Value: "10"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/system/tuning", bytes.NewReader(body))

	handleSystemTuningSet(w, r)

	if w.Code == 400 {
		t.Fatal("valid swappiness should not return 400")
	}
}

func TestHandleSystemTuningSet_ValidJournal(t *testing.T) {
	for _, v := range []string{"true", "false"} {
		body, _ := json.Marshal(tuningSetRequest{Action: "set_persistent_journal", Value: v})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/system/tuning", bytes.NewReader(body))

		handleSystemTuningSet(w, r)

		if w.Code == 400 {
			t.Fatalf("journal=%q should not return 400", v)
		}
	}
}

// --- handleGitCheckout ---

func TestHandleGitCheckout_InvalidRepoDir(t *testing.T) {
	body, _ := json.Marshal(gitCheckoutRequest{RepoDir: "/etc/passwd", Tag: "v0.2.0"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/git/checkout", bytes.NewReader(body))

	handleGitCheckout(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "repo_dir not allowed" {
		t.Fatalf("expected 'repo_dir not allowed', got %q", resp["error"])
	}
}

func TestHandleGitCheckout_EmptyTag(t *testing.T) {
	body, _ := json.Marshal(gitCheckoutRequest{RepoDir: "/repo", Tag: ""})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/git/checkout", bytes.NewReader(body))

	handleGitCheckout(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleGitCheckout_InvalidTagFormat(t *testing.T) {
	tests := []string{"latest", "main", "0.2.0", "v0.2.0; rm -rf /", "v0.2.0\necho pwned"}
	for _, tag := range tests {
		body, _ := json.Marshal(gitCheckoutRequest{RepoDir: "/repo", Tag: tag})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/git/checkout", bytes.NewReader(body))

		handleGitCheckout(w, r)

		if w.Code != 400 {
			t.Fatalf("tag %q: expected 400, got %d", tag, w.Code)
		}
	}
}

func TestHandleGitCheckout_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/git/checkout", bytes.NewReader([]byte("bad")))

	handleGitCheckout(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestIsValidTag(t *testing.T) {
	valid := []string{"v0.1.0", "v1.0", "v0.2.0", "v10.20.30", "v0.3.0-dev.1", "v1.0.0-rc.2"}
	for _, tag := range valid {
		if !isValidTag(tag) {
			t.Errorf("expected %q to be valid", tag)
		}
	}
	invalid := []string{"", "v", "latest", "main", "0.2.0", "v0.2.0; rm -rf /", "v0.2.0\necho pwned", "v0.2.0 DROP TABLE"}
	for _, tag := range invalid {
		if isValidTag(tag) {
			t.Errorf("expected %q to be invalid", tag)
		}
	}
}

func TestIsValidCommitHash(t *testing.T) {
	valid := []string{
		"4bccedb",
		"dbd39954",
		"4bccedb1234567890abcdef1234567890abcdef1",
	}
	for _, s := range valid {
		if !isValidCommitHash(s) {
			t.Errorf("isValidCommitHash(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",                                          // leer
		"4bcced",                                    // 6 Zeichen, zu kurz
		"4bccedb1234567890abcdef1234567890abcdef12", // 41 Zeichen, zu lang
		"4BCCEDB",                                   // Großbuchstaben
		"v1.2.0",                                    // Tag, kein Hash
		"4bccedb; rm -rf /",                         // Shell-Metazeichen
		"../../../etc/passwd",                       // Pfad-Traversal
		"4bccedb\n--upload-pack=evil",               // Newline-Injection
		"-4bccedb",                                  // führender Bindestrich, sieht wie ein Flag aus
	}
	for _, s := range invalid {
		if isValidCommitHash(s) {
			t.Errorf("isValidCommitHash(%q) = true, want false", s)
		}
	}
}

func TestAllowedRepoDirsRejectsTraversal(t *testing.T) {
	allowed := []string{"/repo", "/srv/truffels/data/ckpoolstats"}
	for _, d := range allowed {
		if !isAllowedRepoDir(d) {
			t.Errorf("isAllowedRepoDir(%q) = false, want true", d)
		}
	}

	rejected := []string{
		"/repo/../etc",
		"/srv/truffels/data/ckpoolstats/../../secrets",
		"/srv/truffels/secrets",
		"/repo/",
		"",
		"/",
	}
	for _, d := range rejected {
		if isAllowedRepoDir(d) {
			t.Errorf("isAllowedRepoDir(%q) = true, want false", d)
		}
	}
}

func TestIsValidTagStillRejectsCommitHashes(t *testing.T) {
	// isValidTag darf durch diese Änderung nicht aufgeweicht werden —
	// der Self-Update-Pfad hängt daran.
	if isValidTag("4bccedb") {
		t.Error("isValidTag must keep rejecting bare commit hashes")
	}
	if !isValidTag("v0.3.1-dev.23") {
		t.Error("isValidTag must keep accepting dev tags")
	}
}

// --- handleComposeUpDetached ---

func TestHandleComposeUpDetached_InvalidService(t *testing.T) {
	body, _ := json.Marshal(composeUpDetachedRequest{ServiceID: "evil"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/up-detached", bytes.NewReader(body))

	handleComposeUpDetached(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeUpDetached_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/up-detached", bytes.NewReader([]byte("}")))

	handleComposeUpDetached(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleComposeUpDetached_ValidService(t *testing.T) {
	body, _ := json.Marshal(composeUpDetachedRequest{ServiceID: "truffels-agent"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/up-detached", bytes.NewReader(body))

	handleComposeUpDetached(w, r)

	// 202 if nsenter works, 500 if not (expected in CI)
	if w.Code != 202 && w.Code != 500 {
		t.Fatalf("expected 202 or 500, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if _, ok := resp["status"]; !ok {
		if _, ok2 := resp["error"]; !ok2 {
			t.Fatal("response missing both 'status' and 'error' fields")
		}
	}
}

// --- handleComposeBuild with build args ---

func TestHandleComposeBuild_WithBuildArgs(t *testing.T) {
	// Valid request with build args — will fail at docker compose in CI
	body, _ := json.Marshal(buildRequest{ServiceID: "truffels-agent", BuildArgs: map[string]string{"VERSION": "v0.2.0"}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/build", bytes.NewReader(body))

	handleComposeBuild(w, r)

	// Should not be 400 or 403
	if w.Code == 400 || w.Code == 403 {
		t.Fatalf("expected non-4xx, got %d: %s", w.Code, w.Body.String())
	}
}

// --- stripANSI ---

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"\x1b[2K\r[2026-03-14] 150TH/s", "[2026-03-14] 150TH/s"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"no escapes here", "no escapes here"},
		{"\x1b[2K\rline1\n\x1b[2K\rline2", "line1\nline2"},
		{"", ""},
		// ckpool spinner: CR-separated updates become newline-separated
		{"data1\x1b[2K\rdata2\x1b[2K\rdata3", "data1\ndata2\ndata3"},
		// Real Windows-style \r\n preserved as \n
		{"line1\r\nline2\r\n", "line1\nline2\n"},
	}
	for _, tt := range tests {
		got := stripANSI(tt.input)
		if got != tt.want {
			t.Errorf("stripANSI(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- handleComposeLogs with container filter ---

func TestHandleComposeLogs_ContainerFilter_Allowed(t *testing.T) {
	body, _ := json.Marshal(logsRequest{ServiceID: "ckstats", Tail: 100, Container: "truffels-ckstats-db"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/logs", bytes.NewReader(body))

	handleComposeLogs(w, r)

	// docker logs will fail in CI, but should NOT be 400 or 403
	if w.Code == 400 || w.Code == 403 {
		t.Fatalf("expected non-4xx for allowed container, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleComposeLogs_ContainerFilter_Denied(t *testing.T) {
	body, _ := json.Marshal(logsRequest{ServiceID: "ckstats", Tail: 100, Container: "evil-container"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/logs", bytes.NewReader(body))

	handleComposeLogs(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403 for disallowed container, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "container not allowed: evil-container" {
		t.Fatalf("expected 'container not allowed' error, got %q", resp["error"])
	}
}

func TestHandleComposeLogs_EmptyContainer_UsesCompose(t *testing.T) {
	// Empty container field should use compose logs path (existing behavior)
	body, _ := json.Marshal(logsRequest{ServiceID: "bitcoind", Tail: 50, Container: ""})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/logs", bytes.NewReader(body))

	handleComposeLogs(w, r)

	// Should not be 403 (compose path, not container path)
	if w.Code == 403 {
		t.Fatalf("empty container should use compose path, got 403")
	}
}

// --- handleComposeRewriteTags ---

func TestHandleComposeRewriteTags_Success(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	// Create a fake compose subdir matching the allowlist mapping
	_ = os.MkdirAll(dir+"/mempool", 0755)
	original := `services:
  backend:
    image: mempool/backend:v3.2.0
  frontend:
    image: mempool/frontend:v3.2.0@sha256:abc123
`
	_ = os.WriteFile(dir+"/mempool/docker-compose.yml", []byte(original), 0644)

	body, _ := json.Marshal(rewriteTagsRequest{
		ServiceID: "mempool",
		Images:    []string{"mempool/backend", "mempool/frontend"},
		OldTag:    "v3.2.0",
		NewTag:    "v3.2.1",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/rewrite-tags", bytes.NewReader(body))

	handleComposeRewriteTags(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	data, _ := os.ReadFile(dir + "/mempool/docker-compose.yml")
	content := string(data)
	if !strings.Contains(content, "mempool/backend:v3.2.1") {
		t.Errorf("expected backend updated to v3.2.1, got:\n%s", content)
	}
	if !strings.Contains(content, "mempool/frontend:v3.2.1") {
		t.Errorf("expected frontend updated to v3.2.1 (digest stripped), got:\n%s", content)
	}
	if strings.Contains(content, "v3.2.0") {
		t.Errorf("old version should not remain, got:\n%s", content)
	}
	if strings.Contains(content, "sha256") {
		t.Errorf("digest should be stripped, got:\n%s", content)
	}
}

func TestHandleComposeRewriteTags_InvalidService(t *testing.T) {
	body, _ := json.Marshal(rewriteTagsRequest{
		ServiceID: "hacker",
		Images:    []string{"img"},
		OldTag:    "v1",
		NewTag:    "v2",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/rewrite-tags", bytes.NewReader(body))

	handleComposeRewriteTags(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeRewriteTags_MissingFields(t *testing.T) {
	body, _ := json.Marshal(rewriteTagsRequest{
		ServiceID: "mempool",
		Images:    []string{},
		NewTag:    "v2",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/rewrite-tags", bytes.NewReader(body))

	handleComposeRewriteTags(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleComposeRewriteTags_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/rewrite-tags", bytes.NewReader([]byte("bad")))

	handleComposeRewriteTags(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- version in health ---

func TestHealthIncludesVersion(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/health", nil)
	handleHealth(w, r)

	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["version"] == "" {
		t.Fatal("expected version field in health response")
	}
	if body["version"] != "dev" {
		// Default should be "dev" when not built with ldflags
		t.Fatalf("expected 'dev', got %q", body["version"])
	}
}

// --- handleComposeRewriteTags: VERSION args + change detection ---

func TestRewriteTags_UpdatesVersionArgs(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	_ = os.MkdirAll(dir+"/truffels", 0755)
	original := `services:
  agent:
    build:
      args:
        VERSION: v0.2.2
    image: truffels/agent:v0.2.2
  api:
    build:
      args:
        VERSION: v0.2.2
    image: truffels/api:v0.2.2
  web:
    build:
      args:
        VERSION: v0.2.2
    image: truffels/web:v0.2.2
`
	_ = os.WriteFile(dir+"/truffels/docker-compose.yml", []byte(original), 0644)

	body, _ := json.Marshal(rewriteTagsRequest{
		ServiceID: "truffels-agent",
		Images:    []string{"truffels/agent", "truffels/api", "truffels/web"},
		NewTag:    "v0.3.0",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/rewrite-tags", bytes.NewReader(body))

	handleComposeRewriteTags(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	data, _ := os.ReadFile(dir + "/truffels/docker-compose.yml")
	content := string(data)
	if strings.Contains(content, "v0.2.2") {
		t.Errorf("old version should not remain, got:\n%s", content)
	}
	if !strings.Contains(content, "VERSION: v0.3.0") {
		t.Errorf("expected VERSION args updated to v0.3.0, got:\n%s", content)
	}
	if !strings.Contains(content, "truffels/agent:v0.3.0") {
		t.Errorf("expected image tag updated to v0.3.0, got:\n%s", content)
	}
}

func TestRewriteTags_IdempotentMatchesAnyTag(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	_ = os.MkdirAll(dir+"/truffels", 0755)
	original := `services:
  agent:
    image: truffels/agent:v0.1.0
`
	_ = os.WriteFile(dir+"/truffels/docker-compose.yml", []byte(original), 0644)

	// OldTag omitted — should match any current tag and rewrite to new
	body, _ := json.Marshal(rewriteTagsRequest{
		ServiceID: "truffels-agent",
		Images:    []string{"truffels/agent"},
		NewTag:    "v1.0.0",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/rewrite-tags", bytes.NewReader(body))

	handleComposeRewriteTags(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(dir + "/truffels/docker-compose.yml")
	content := string(data)
	if !strings.Contains(content, "truffels/agent:v1.0.0") {
		t.Errorf("expected tag updated to v1.0.0, got:\n%s", content)
	}
	if strings.Contains(content, "v0.1.0") {
		t.Errorf("old tag should be gone, got:\n%s", content)
	}
}

func TestRewriteTags_NoMatchReturnsError(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	_ = os.MkdirAll(dir+"/truffels", 0755)
	// Image name doesn't match any in the file
	original := `services:
  agent:
    image: truffels/agent:v0.1.0
`
	_ = os.WriteFile(dir+"/truffels/docker-compose.yml", []byte(original), 0644)

	body, _ := json.Marshal(rewriteTagsRequest{
		ServiceID: "truffels-agent",
		Images:    []string{"truffels/nonexistent"},
		NewTag:    "v1.0.0",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/rewrite-tags", bytes.NewReader(body))

	handleComposeRewriteTags(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400 for no match, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "no image tags matched") {
		t.Errorf("expected 'no image tags matched' error, got: %s", resp["error"])
	}
}

func TestRewriteTags_AlreadyAtTargetReturnsOK(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	_ = os.MkdirAll(dir+"/truffels", 0755)
	original := `services:
  agent:
    image: truffels/agent:v1.0.0
    build:
      args:
        VERSION: v1.0.0
`
	_ = os.WriteFile(dir+"/truffels/docker-compose.yml", []byte(original), 0644)

	body, _ := json.Marshal(rewriteTagsRequest{
		ServiceID: "truffels-agent",
		Images:    []string{"truffels/agent"},
		NewTag:    "v1.0.0",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/rewrite-tags", bytes.NewReader(body))

	handleComposeRewriteTags(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200 for already-at-target, got %d: %s", w.Code, w.Body.String())
	}
}

// --- handleImageRemove ---

func TestHandleImageRemove_AllowedImage(t *testing.T) {
	reqBody, _ := json.Marshal(map[string]string{"image": "truffels/api:v0.3.0-dev.1"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/remove", bytes.NewReader(reqBody))

	handleImageRemove(w, r)

	// docker rmi will fail in CI (image doesn't exist), but should return 200 (best-effort)
	if w.Code != 200 {
		t.Fatalf("expected 200 (best-effort), got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleImageRemove_DeniedImage(t *testing.T) {
	reqBody, _ := json.Marshal(map[string]string{"image": "nginx:latest"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/remove", bytes.NewReader(reqBody))

	handleImageRemove(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "image not allowed" {
		t.Fatalf("expected 'image not allowed', got %q", body["error"])
	}
}

func TestHandleImageRemove_EmptyImage(t *testing.T) {
	reqBody, _ := json.Marshal(map[string]string{"image": ""})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/remove", bytes.NewReader(reqBody))

	handleImageRemove(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleImageRemove_MalformedJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/image/remove", bytes.NewReader([]byte("bad")))

	handleImageRemove(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleImageRemove_AllowedPrefixes(t *testing.T) {
	allowed := []string{
		"truffels/agent:v1.0", "mempool/backend:v3.2", "btcpayserver/bitcoin:30",
		"getumbrel/electrs:v0.11", "caddy:2.11", "postgres:16", "mariadb:lts",
	}
	for _, img := range allowed {
		reqBody, _ := json.Marshal(map[string]string{"image": img})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/image/remove", bytes.NewReader(reqBody))
		handleImageRemove(w, r)
		if w.Code == 403 {
			t.Errorf("image %q should be allowed, got 403", img)
		}
	}
}

// --- formatSize ---

func TestFormatSize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"111.2GB", "111.2 GB"},
		{"1.8T", "1.8 T"},
		{"0B", "0 B"},
		{"8.0M", "8.0 M"},
		{"52%", "52%"},
		{"111.2 GB", "111.2 GB"}, // idempotent
		{"", ""},
	}
	for _, tt := range tests {
		got := formatSize(tt.input)
		if got != tt.want {
			t.Errorf("formatSize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- handleDockerPruneBuildCache ---

func TestHandleDockerPruneBuildCache_ReturnsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/docker/prune-buildcache", bytes.NewReader([]byte("{}")))

	handleDockerPruneBuildCache(w, r)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	// Either "ok" (docker available) or error (docker not available in CI)
	if body["status"] != "ok" && body["error"] == "" {
		t.Fatalf("expected status ok or error field, got %v", body)
	}
}

// --- handleDockerPrune ---

func TestHandleDockerPrune_ReturnsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/docker/prune", bytes.NewReader([]byte("{}")))

	handleDockerPrune(w, r)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	// docker commands will fail in CI but should still return JSON
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %q", body["status"])
	}
}

// --- Compose Read ---

func TestHandleComposeRead_Success(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	_ = os.MkdirAll(dir+"/ckpool", 0755)
	content := "services:\n  ckpool:\n    image: truffels/ckpool:v1.0.0\n"
	_ = os.WriteFile(dir+"/ckpool/docker-compose.yml", []byte(content), 0644)

	body, _ := json.Marshal(serviceRequest{ServiceID: "ckpool"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/read", bytes.NewReader(body))

	handleComposeRead(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["content"] != content {
		t.Fatalf("expected content %q, got %q", content, resp["content"])
	}
}

func TestHandleComposeRead_InvalidService(t *testing.T) {
	body, _ := json.Marshal(serviceRequest{ServiceID: "hacker"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/read", bytes.NewReader(body))

	handleComposeRead(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	body, _ := json.Marshal(serviceRequest{ServiceID: "ckpool"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/read", bytes.NewReader(body))

	handleComposeRead(w, r)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// --- Compose Reconcile ---

func TestHandleComposeReconcile_NoChange(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	_ = os.MkdirAll(dir+"/ckpool", 0755)
	content := "services:\n  ckpool:\n    image: truffels/ckpool:v1.0.0\n"
	_ = os.WriteFile(dir+"/ckpool/docker-compose.yml", []byte(content), 0644)

	body, _ := json.Marshal(map[string]string{
		"service_id":       "ckpool",
		"expected_content": content,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/reconcile", bytes.NewReader(body))

	handleComposeReconcile(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["changed"] != false {
		t.Fatal("expected changed=false")
	}
}

func TestHandleComposeReconcile_Changed(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	_ = os.MkdirAll(dir+"/ckpool", 0755)
	oldContent := "memory: 256M\n"
	newContent := "memory: 1024M\n"
	_ = os.WriteFile(dir+"/ckpool/docker-compose.yml", []byte(oldContent), 0644)

	body, _ := json.Marshal(map[string]string{
		"service_id":       "ckpool",
		"expected_content": newContent,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/reconcile", bytes.NewReader(body))

	handleComposeReconcile(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["changed"] != true {
		t.Fatal("expected changed=true")
	}

	// Verify file was written
	data, _ := os.ReadFile(dir + "/ckpool/docker-compose.yml")
	if string(data) != newContent {
		t.Fatalf("expected file to contain new content, got: %s", string(data))
	}
}

func TestHandleComposeReconcile_InvalidService(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"service_id":       "hacker",
		"expected_content": "anything",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/reconcile", bytes.NewReader(body))

	handleComposeReconcile(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleComposeReconcile_EmptyContent(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"service_id":       "ckpool",
		"expected_content": "",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/compose/reconcile", bytes.NewReader(body))

	handleComposeReconcile(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- File Reconcile ---

func TestHandleFileReconcile_Changed(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	_ = os.MkdirAll(dir+"/ckstats", 0755)
	_ = os.WriteFile(dir+"/ckstats/Dockerfile", []byte("FROM node:20-slim\n"), 0644)

	body, _ := json.Marshal(map[string]string{
		"path":             "ckstats/Dockerfile",
		"expected_content": "FROM node:22-slim\n",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/file/reconcile", bytes.NewReader(body))

	handleFileReconcile(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["changed"] != true {
		t.Fatal("expected changed=true")
	}

	data, _ := os.ReadFile(dir + "/ckstats/Dockerfile")
	if string(data) != "FROM node:22-slim\n" {
		t.Fatalf("expected updated content, got: %s", string(data))
	}
}

func TestHandleFileReconcile_Unchanged(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	content := "FROM node:22-slim\n"
	_ = os.MkdirAll(dir+"/ckstats", 0755)
	_ = os.WriteFile(dir+"/ckstats/Dockerfile", []byte(content), 0644)

	body, _ := json.Marshal(map[string]string{
		"path":             "ckstats/Dockerfile",
		"expected_content": content,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/file/reconcile", bytes.NewReader(body))

	handleFileReconcile(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["changed"] != false {
		t.Fatal("expected changed=false")
	}
}

func TestHandleFileReconcile_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	body, _ := json.Marshal(map[string]string{
		"path":             "../etc/passwd",
		"expected_content": "hacked",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/file/reconcile", bytes.NewReader(body))

	handleFileReconcile(w, r)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleFileReconcile_EmptyFields(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"path":             "",
		"expected_content": "",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/file/reconcile", bytes.NewReader(body))

	handleFileReconcile(w, r)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleFileReconcile_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir

	_ = os.MkdirAll(dir+"/ckpool", 0755)

	body, _ := json.Marshal(map[string]string{
		"path":             "ckpool/Dockerfile",
		"expected_content": "FROM debian:bookworm-slim\n",
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/file/reconcile", bytes.NewReader(body))

	handleFileReconcile(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["changed"] != true {
		t.Fatal("expected changed=true for new file")
	}

	data, _ := os.ReadFile(dir + "/ckpool/Dockerfile")
	if string(data) != "FROM debian:bookworm-slim\n" {
		t.Fatalf("expected content written, got: %s", string(data))
	}
}

// --- fs/ensure-dir ---

func postEnsureDir(t *testing.T, path string, uid, gid int, mode string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"path": path, "uid": uid, "gid": gid, "mode": mode,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/fs/ensure-dir", bytes.NewReader(body))
	handleEnsureDir(w, r)
	return w
}

func TestHandleEnsureDir_CreatesIdempotent(t *testing.T) {
	dir := t.TempDir()
	dataRoot = dir

	// Use the test runner's own uid/gid so chown is a no-op — production
	// agent runs as root and can chown to 1000:1000, but CI runners can't.
	// We're exercising the path validation + mkdir paths, not privilege.
	uid, gid := os.Getuid(), os.Getgid()

	target := dir + "/mempool/cache"
	w := postEnsureDir(t, target, uid, gid, "0755")
	if w.Code != 200 {
		t.Fatalf("first call: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Fatalf("dir not created: %v", err)
	}
	// Idempotent: second call also OK.
	w = postEnsureDir(t, target, uid, gid, "0755")
	if w.Code != 200 {
		t.Fatalf("second call: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleEnsureDir_RejectsOutsideDataRoot(t *testing.T) {
	dir := t.TempDir()
	dataRoot = dir

	// Absolute escape
	w := postEnsureDir(t, "/etc/eviltest", 1000, 1000, "0755")
	if w.Code != 403 {
		t.Errorf("absolute escape: expected 403, got %d", w.Code)
	}
	// Relative escape via ..
	w = postEnsureDir(t, dir+"/foo/../../etcbad", 1000, 1000, "0755")
	if w.Code != 403 {
		t.Errorf("..-escape: expected 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleEnsureDir_RejectsEmptyPath(t *testing.T) {
	dataRoot = t.TempDir()
	w := postEnsureDir(t, "", 1000, 1000, "0755")
	if w.Code != 403 {
		t.Errorf("expected 403 for empty path, got %d", w.Code)
	}
}

func TestHandleEnsureDir_RejectsSymlinkRedirect(t *testing.T) {
	dir := t.TempDir()
	dataRoot = dir
	// Create a symlink at dataRoot/evil pointing to /tmp (outside dataRoot).
	outside := t.TempDir() // real path outside dataRoot
	if err := os.Symlink(outside, dir+"/evil"); err != nil {
		t.Skip("symlink not supported: ", err)
	}
	// Now try to ensure-dir at dataRoot/evil/foo — parent (evil) resolves outside dataRoot.
	w := postEnsureDir(t, dir+"/evil/foo", 1000, 1000, "0755")
	if w.Code != 403 {
		t.Errorf("expected 403 for symlink redirect, got %d body=%s", w.Code, w.Body.String())
	}
}

// --- fs/clear-dir ---

func postClearDir(t *testing.T, path string, uid, gid int, mode string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"path": path, "uid": uid, "gid": gid, "mode": mode,
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/fs/clear-dir", bytes.NewReader(body))
	handleClearDir(w, r)
	return w
}

func TestHandleClearDir_HappyPath(t *testing.T) {
	dir := t.TempDir()
	dataRoot = dir
	target := dir + "/mempool/cache"
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	// drop a file inside
	if err := os.WriteFile(target+"/rbfcache.json", []byte("xx"), 0644); err != nil {
		t.Fatal(err)
	}
	// Use the test runner's own uid/gid (see TestHandleEnsureDir_CreatesIdempotent).
	uid, gid := os.Getuid(), os.Getgid()
	w := postClearDir(t, target, uid, gid, "0755")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(target + "/rbfcache.json"); !os.IsNotExist(err) {
		t.Error("expected rbfcache.json removed")
	}
	if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
		t.Errorf("expected dir to be recreated: %v", err)
	}
}

func TestHandleClearDir_RejectsBasenameNotInAllowlist(t *testing.T) {
	dir := t.TempDir()
	dataRoot = dir
	target := dir + "/bitcoin/blockchain"
	_ = os.MkdirAll(target, 0755)
	w := postClearDir(t, target, 1000, 1000, "0755")
	if w.Code != 403 {
		t.Errorf("expected 403 for blockchain basename, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Error("dir must NOT have been removed")
	}
}

// The "blockchain-suffix-trick": even if basename matches "cache", the path is
// rejected if not exactly two levels under dataRoot. This guards against
// requests like /srv/truffels/data/bitcoin/blockchain/cache.
func TestHandleClearDir_RejectsThreeLevelDeep(t *testing.T) {
	dir := t.TempDir()
	dataRoot = dir
	target := dir + "/bitcoin/blockchain/cache"
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	w := postClearDir(t, target, 1000, 1000, "0755")
	if w.Code != 403 {
		t.Errorf("expected 403 for three-level-deep path, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Error("dir must NOT have been removed")
	}
}

func TestHandleClearDir_RejectsAbsoluteEscape(t *testing.T) {
	dataRoot = t.TempDir()
	w := postClearDir(t, "/etc/cache", 1000, 1000, "0755")
	if w.Code != 403 {
		t.Errorf("expected 403 for absolute escape, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleClearDir_RejectsRelativeEscape(t *testing.T) {
	dir := t.TempDir()
	dataRoot = dir
	target := dir + "/foo/../../etc/cache"
	w := postClearDir(t, target, 1000, 1000, "0755")
	if w.Code != 403 {
		t.Errorf("expected 403 for relative escape, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestValidateUnderRoot_AllowsMissingRoot(t *testing.T) {
	// Replicate the production bug: the root itself doesn't exist (the agent
	// container has no /srv/truffels/data mount). validateUnderRoot must walk
	// past root up to the nearest existing ancestor and not flag it as a symlink
	// redirect just because the ancestor doesn't match root.
	tmpBase := t.TempDir()
	root := tmpBase + "/notyet" // root itself doesn't exist
	target := root + "/mempool/cache/subdir"
	cleaned, err := validateUnderRoot(target, root)
	if err != nil {
		t.Fatalf("expected accept, got error: %v", err)
	}
	if cleaned != target {
		t.Errorf("expected cleaned=%q, got %q", target, cleaned)
	}
}

func TestValidateUnderRoot_AllowsNestedUnderExistingRoot(t *testing.T) {
	root := t.TempDir() // root exists
	target := root + "/foo/bar/baz"
	cleaned, err := validateUnderRoot(target, root)
	if err != nil {
		t.Fatalf("expected accept, got error: %v", err)
	}
	if cleaned != target {
		t.Errorf("expected cleaned=%q, got %q", target, cleaned)
	}
}

// --- dirSizeCache ---

func TestDirSizeCache_HitMiss(t *testing.T) {
	c := newDirSizeCache()
	if _, _, hit := c.get("/foo"); hit {
		t.Fatal("expected miss before set")
	}
	c.set("/foo", 42)
	size, walked, hit := c.get("/foo")
	if !hit {
		t.Fatal("expected hit after set")
	}
	if size != 42 {
		t.Errorf("size: %d", size)
	}
	if walked.IsZero() {
		t.Error("walked timestamp should be non-zero")
	}
}

func TestDirSizeCache_ForgetRemoves(t *testing.T) {
	c := newDirSizeCache()
	c.set("/foo", 1)
	c.set("/bar", 2)
	c.forget("/foo")
	if _, _, hit := c.get("/foo"); hit {
		t.Error("expected forget to remove the entry")
	}
	if _, _, hit := c.get("/bar"); !hit {
		t.Error("forget must not affect other entries")
	}
}

func TestHandleFileReconcile_AcceptsConfigRoot(t *testing.T) {
	dir := t.TempDir()
	composeRoot = dir + "/compose"
	configRoot = dir + "/config"
	if err := os.MkdirAll(composeRoot+"/proxy", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configRoot+"/proxy", 0755); err != nil {
		t.Fatal(err)
	}

	// composeRoot path — should still work.
	body := `{"path":"` + composeRoot + `/proxy/docker-compose.yml","expected_content":"x: y\n"}`
	r := httptest.NewRequest("POST", "/v1/file/reconcile", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleFileReconcile(w, r)
	if w.Code != 200 {
		t.Fatalf("composeRoot: expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	// configRoot path — must also work.
	body = `{"path":"` + configRoot + `/proxy/Caddyfile","expected_content":"z\n"}`
	r = httptest.NewRequest("POST", "/v1/file/reconcile", strings.NewReader(body))
	w = httptest.NewRecorder()
	handleFileReconcile(w, r)
	if w.Code != 200 {
		t.Fatalf("configRoot: expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	// /etc/passwd — must reject.
	body = `{"path":"/etc/passwd","expected_content":"x\n"}`
	r = httptest.NewRequest("POST", "/v1/file/reconcile", strings.NewReader(body))
	w = httptest.NewRecorder()
	handleFileReconcile(w, r)
	if w.Code != 403 {
		t.Errorf("/etc/passwd: expected 403, got %d", w.Code)
	}
}

// dev.16: walker must index both 1-level service dirs (ckpool, truffels)
// and 2-level leaves (bitcoin/blockchain, electrs/db). dev.15 only did 2-level
// when children existed, so ckpool's template path got a cache miss.
func TestWalkDataDirs_IndexesTopLevelAndLeaves(t *testing.T) {
	root := t.TempDir()
	// Build a fake data tree: ckpool/logs (children exist) + truffels (also children)
	// and a 2-level case bitcoin/blockchain.
	for _, p := range []string{
		root + "/ckpool/logs/pool",
		root + "/truffels/somefile_parent",
		root + "/bitcoin/blockchain",
	} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}

	c := newDirSizeCache()
	// Drive a single walk iteration synchronously by inlining the body.
	// Easier than waiting 5s + 5min from the goroutine.
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		servicePath := root + "/" + e.Name()
		c.set(servicePath, dirSizeBytes(servicePath))
		children, _ := os.ReadDir(servicePath)
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			c.set(servicePath+"/"+child.Name(), dirSizeBytes(servicePath+"/"+child.Name()))
		}
	}

	// All four paths should be in the cache.
	wanted := []string{
		root + "/ckpool",
		root + "/ckpool/logs",
		root + "/truffels",
		root + "/bitcoin",
		root + "/bitcoin/blockchain",
	}
	for _, p := range wanted {
		if _, _, hit := c.get(p); !hit {
			t.Errorf("expected cache hit for %q", p)
		}
	}
}

func TestDockerStorageCache_HitMiss(t *testing.T) {
	c := newDockerStorageCache(100 * time.Millisecond)
	if _, hit := c.get(); hit {
		t.Fatal("expected miss before set")
	}
	c.set([]dockerStorageItem{{Type: "Images", Count: 5}})
	if items, hit := c.get(); !hit || len(items) != 1 || items[0].Count != 5 {
		t.Fatalf("expected hit after set; got hit=%v items=%v", hit, items)
	}
	time.Sleep(150 * time.Millisecond)
	if _, hit := c.get(); hit {
		t.Error("expected miss after TTL expiry")
	}
}

func TestDockerStorageCache_InvalidateClearsCache(t *testing.T) {
	c := newDockerStorageCache(1 * time.Hour)
	c.set([]dockerStorageItem{{Type: "Images", Count: 5}})
	if _, hit := c.get(); !hit {
		t.Fatal("expected hit after set")
	}
	c.invalidate()
	if _, hit := c.get(); hit {
		t.Error("expected miss after invalidate")
	}
}

func TestDirSizeCache_StaleMarkerSurfacedViaTimestamp(t *testing.T) {
	c := newDirSizeCache()
	c.set("/foo", 12345)
	c.mu.Lock()
	c.walked["/foo"] = time.Now().Add(-2 * time.Hour)
	c.mu.Unlock()
	_, walked, hit := c.get("/foo")
	if !hit {
		t.Fatal("expected hit")
	}
	if time.Since(walked) < time.Hour {
		t.Errorf("walked was %v ago, expected >1h", time.Since(walked))
	}
}

func TestImageInspectResponseCarriesLabels(t *testing.T) {
	// Die Response-Struktur muss ein labels-Feld serialisieren, sonst kann
	// die API die gebaute Ref nicht zurücklesen.
	resp := imageInspectResponse{
		Image:  "truffels/ckpool:latest",
		Labels: map[string]string{"org.truffels.source-ref": "v1.2.0"},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"labels"`) {
		t.Errorf("response JSON lacks labels field: %s", b)
	}
	if !strings.Contains(string(b), "org.truffels.source-ref") {
		t.Errorf("response JSON lacks the source-ref label: %s", b)
	}
}
