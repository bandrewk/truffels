package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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
