package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
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
	json.Unmarshal(w.Body.Bytes(), &body)
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
		"proxy", "truffels-agent", "truffels-api", "truffels-web",
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
	json.Unmarshal(w.Body.Bytes(), &states)
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
	json.Unmarshal(w.Body.Bytes(), &body)
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
