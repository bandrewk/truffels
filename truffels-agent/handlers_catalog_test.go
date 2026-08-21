package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHandleCatalogGet(t *testing.T) {
	var err error
	loadedCatalog, err = LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rec := httptest.NewRecorder()
	handleCatalogGet(rec, httptest.NewRequest(http.MethodGet, "/v1/catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d", rec.Code)
	}
	var got map[string]catalogEntryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Response not decodable: %v", err)
	}
	if _, ok := got["digibyted"]; !ok {
		t.Error("digibyted missing from response")
	}
	// Verify that each entry includes container_names derived from catContainerName
	entry := got["digibyted"]
	if len(entry.ContainerNames) == 0 {
		t.Error("digibyted entry has no container_names")
	}
	// Check that at least one container name matches the expected pattern
	found := false
	for _, cn := range entry.ContainerNames {
		if cn == "truffels-digibyted-node" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected container name truffels-digibyted-node not found in %v", entry.ContainerNames)
	}
}

func applyReq(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/service/apply", bytes.NewBufferString(body))
	handleServiceApply(rec, req)
	return rec
}

func TestServiceApplyRejectsUnknownID(t *testing.T) {
	loadedCatalog, _ = LoadCatalog()
	rec := applyReq(t, `{"id":"not-in-catalog","params":{}}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Code = %d, expected 403", rec.Code)
	}
}

func TestServiceApplyRejectsBadIDCharset(t *testing.T) {
	loadedCatalog, _ = LoadCatalog()
	for _, bad := range []string{"../bitcoin", "Digibyted", "dgb node", "cat-../../etc"} {
		rec := applyReq(t, `{"id":"`+bad+`","params":{}}`)
		if rec.Code == http.StatusOK {
			t.Errorf("id %q was accepted", bad)
		}
	}
}

func TestServiceApplyRejectsBadParamBeforeWriting(t *testing.T) {
	loadedCatalog, _ = LoadCatalog()
	rec := applyReq(t, `{"id":"digibyted","params":{"prune_gb":99999}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Code = %d, expected 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "prune_gb") {
		t.Errorf("Error message does not mention parameter: %s", rec.Body.String())
	}
}

func TestServiceRemoveKeepsDataByDefault(t *testing.T) {
	loadedCatalog, _ = LoadCatalog()
	tmp := t.TempDir()
	composeRoot, dataRoot, configRoot = tmp+"/compose", tmp+"/data", tmp+"/config"
	defer func() {
		composeRoot, dataRoot, configRoot = "/srv/truffels/compose", "/srv/truffels/data", "/srv/truffels/config"
	}()

	// Create data file that should persist after remove with purge_data=false
	if err := os.MkdirAll(catDataDir("digibyted"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := catDataDir("digibyted") + "/important.dat"
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create config file that should be removed (it is a rendered artifact)
	configDir := catConfigDir("digibyted")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configMarker := catConfigPath("digibyted", "digibyte.conf")
	if err := os.WriteFile(configMarker, []byte("conf"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handleServiceRemove(rec, httptest.NewRequest(http.MethodPost, "/v1/service/remove",
		bytes.NewBufferString(`{"id":"digibyted","purge_data":false}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d: %s", rec.Code, rec.Body.String())
	}
	// Config file must be removed (it is rendered artifact from catalog)
	if _, err := os.Stat(configMarker); !os.IsNotExist(err) {
		t.Error("Config file was not removed even though it is a catalog artifact")
	}
	// Data file must remain (user data preserved)
	if _, err := os.Stat(marker); err != nil {
		t.Error("Data was removed even though purge_data=false")
	}
}

func TestServiceRemovePurgesOnlyWhenAsked(t *testing.T) {
	loadedCatalog, _ = LoadCatalog()
	tmp := t.TempDir()
	composeRoot, dataRoot, configRoot = tmp+"/compose", tmp+"/data", tmp+"/config"
	defer func() {
		composeRoot, dataRoot, configRoot = "/srv/truffels/compose", "/srv/truffels/data", "/srv/truffels/config"
	}()
	_ = os.MkdirAll(catDataDir("digibyted"), 0o755)
	marker := catDataDir("digibyted") + "/important.dat"
	_ = os.WriteFile(marker, []byte("x"), 0o644)

	rec := httptest.NewRecorder()
	handleServiceRemove(rec, httptest.NewRequest(http.MethodPost, "/v1/service/remove",
		bytes.NewBufferString(`{"id":"digibyted","purge_data":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("Data was not removed even though purge_data=true")
	}
}

func TestServiceRemoveSkipsDownWithoutComposeFile(t *testing.T) {
	loadedCatalog, _ = LoadCatalog()
	tmp := t.TempDir()
	composeRoot, dataRoot, configRoot = tmp+"/compose", tmp+"/data", tmp+"/config"
	defer func() {
		composeRoot, dataRoot, configRoot = "/srv/truffels/compose", "/srv/truffels/data", "/srv/truffels/config"
	}()
	// No compose directory created — remove must not attempt to call docker.
	// Returns 200 and removes config/data as usual.
	rec := httptest.NewRecorder()
	handleServiceRemove(rec, httptest.NewRequest(http.MethodPost, "/v1/service/remove",
		bytes.NewBufferString(`{"id":"digibyted","purge_data":false}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestServiceApplyWritesFiles(t *testing.T) {
	loadedCatalog, _ = LoadCatalog()
	tmp := t.TempDir()
	composeRoot, dataRoot, configRoot = tmp+"/compose", tmp+"/data", tmp+"/config"
	defer func() {
		composeRoot, dataRoot, configRoot = "/srv/truffels/compose", "/srv/truffels/data", "/srv/truffels/config"
	}()

	rec := httptest.NewRecorder()
	handleServiceApply(rec, httptest.NewRequest(http.MethodPost, "/v1/service/apply",
		bytes.NewBufferString(`{"id":"digibyted","params":{}}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("Code = %d: %s", rec.Code, rec.Body.String())
	}

	// Check (a): compose file exists and contains container name
	composeFile := catComposeDir("digibyted") + "/docker-compose.yml"
	if _, err := os.Stat(composeFile); err != nil {
		t.Fatalf("Compose file does not exist: %v", err)
	}
	composeData, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("Cannot read compose file: %v", err)
	}
	if !strings.Contains(string(composeData), "truffels-digibyted-node") {
		t.Error("Compose file does not contain expected container name truffels-digibyted-node")
	}

	// Check (b): config file exists and contains required settings
	configFile := catConfigPath("digibyted", "digibyte.conf")
	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("Config file does not exist: %v", err)
	}
	configData, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("Cannot read config file: %v", err)
	}
	configContent := string(configData)
	if !strings.Contains(configContent, "algo=sha256d") {
		t.Error("Config file does not contain algo=sha256d")
	}
	if !strings.Contains(configContent, "txindex=1") {
		t.Error("Config file does not contain txindex=1 (required by this build's DigiDollar)")
	}

	// Check (c): data directory exists
	dataDir := catDataDir("digibyted")
	if stat, err := os.Stat(dataDir); err != nil {
		t.Fatalf("Data directory does not exist: %v", err)
	} else if !stat.IsDir() {
		t.Error("Data path exists but is not a directory")
	}
}

func TestParseNumericUser(t *testing.T) {
	cases := []struct {
		in       string
		uid, gid int
		ok       bool
	}{
		{"1000:1000", 1000, 1000, true},
		{"1000", 1000, 1000, true}, // bare uid → gid defaults to uid
		{"0:0", 0, 0, true},
		{"", 0, 0, false},       // no user set
		{"nobody", 0, 0, false}, // non-numeric
		{"1000:grp", 0, 0, false},
	}
	for _, c := range cases {
		uid, gid, ok := parseNumericUser(c.in)
		if ok != c.ok || (ok && (uid != c.uid || gid != c.gid)) {
			t.Errorf("parseNumericUser(%q) = (%d,%d,%v), want (%d,%d,%v)", c.in, uid, gid, ok, c.uid, c.gid, c.ok)
		}
	}
}

// The data dir owner is the user of the container that mounts the data volume.
func TestDataDirOwner(t *testing.T) {
	e := CatalogEntry{Containers: []ContainerSpec{
		{Name: "aux", User: "0:0", Volumes: []VolumeSpec{{Kind: "config", File: "x.conf", Mount: "/x"}}},
		{Name: "node", User: "1000:1000", Volumes: []VolumeSpec{{Kind: "data", Mount: "/data"}}},
	}}
	uid, gid, ok := dataDirOwner(e)
	if !ok || uid != 1000 || gid != 1000 {
		t.Errorf("dataDirOwner = (%d,%d,%v), want (1000,1000,true)", uid, gid, ok)
	}
	// No data volume anywhere → no owner to assign.
	none := CatalogEntry{Containers: []ContainerSpec{{Name: "x", User: "1000:1000"}}}
	if _, _, ok := dataDirOwner(none); ok {
		t.Error("dataDirOwner should be false when no container mounts a data volume")
	}
}

func chainProbeReq(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handleServiceChainProbe(rec, httptest.NewRequest(http.MethodPost, "/v1/service/chain-probe", bytes.NewBufferString(body)))
	return rec
}

// The chain probe must reject anything it cannot map to a curated probe before
// it ever runs a command: bad ids, unknown entries, and entries with no probe.
func TestChainProbeGuards(t *testing.T) {
	loadedCatalog, _ = LoadCatalog()

	if rec := chainProbeReq(t, `{"id":"../x"}`); rec.Code != http.StatusForbidden {
		t.Errorf("invalid id: code=%d", rec.Code)
	}
	if rec := chainProbeReq(t, `{"id":"not-in-catalog"}`); rec.Code != http.StatusForbidden {
		t.Errorf("unknown entry: code=%d", rec.Code)
	}

	// An entry without a declared probe must be refused, not have a command guessed.
	loadedCatalog = Catalog{"noprobe": CatalogEntry{ID: "noprobe", Containers: []ContainerSpec{{Name: "n", User: "1000:1000"}}}}
	if rec := chainProbeReq(t, `{"id":"noprobe"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("no-probe entry: code=%d body=%s", rec.Code, rec.Body.String())
	}

	loadedCatalog, _ = LoadCatalog()
}

// The digibyted entry must actually carry a sync probe, or the API can never
// render its sync progress.
func TestDigibytedHasSyncProbe(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	e := cat["digibyted"]
	if e.ChainInfo == nil || len(e.ChainInfo.SyncProbe) == 0 {
		t.Fatal("digibyted has no chain_info.sync_probe")
	}
	if e.ChainInfo.SyncProbe[len(e.ChainInfo.SyncProbe)-1] != "getblockchaininfo" {
		t.Errorf("sync probe should end in getblockchaininfo, got %v", e.ChainInfo.SyncProbe)
	}
}
