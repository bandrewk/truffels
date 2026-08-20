package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHasCatalogSegment(t *testing.T) {
	yes := []string{
		"/srv/truffels/compose/cat-digibyted/docker-compose.yml",
		"/srv/truffels/config/cat-digibyted/digibyte.conf",
		"cat-digibyted/docker-compose.yml",
		"/srv/truffels/data/cat-x",
	}
	no := []string{
		"/srv/truffels/compose/bitcoin/docker-compose.yml",
		"/srv/truffels/data/duplicate-cat-pictures", // substring, not a segment prefix
		"/srv/truffels/data/bitcoin/catalog",        // segment does not start with cat-
		"proxy/Caddyfile",
	}
	for _, p := range yes {
		if !hasCatalogSegment(p) {
			t.Errorf("hasCatalogSegment(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if hasCatalogSegment(p) {
			t.Errorf("hasCatalogSegment(%q) = true, want false", p)
		}
	}
}

func TestFileReconcileRejectsCatalogPath(t *testing.T) {
	tmp := t.TempDir()
	composeRoot, configRoot = tmp+"/compose", tmp+"/config"
	defer func() {
		composeRoot, configRoot = "/srv/truffels/compose", "/srv/truffels/config"
	}()
	_ = os.MkdirAll(composeRoot+"/cat-digibyted", 0o755)

	rec := httptest.NewRecorder()
	body := `{"path":"cat-digibyted/docker-compose.yml","expected_content":"pwned"}`
	handleFileReconcile(rec, httptest.NewRequest(http.MethodPost, "/v1/file/reconcile", bytes.NewBufferString(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(composeRoot + "/cat-digibyted/docker-compose.yml"); !os.IsNotExist(err) {
		t.Fatal("file/reconcile wrote into a catalog directory")
	}
}

func TestClearDirRejectsCatalogPath(t *testing.T) {
	tmp := t.TempDir()
	dataRoot = tmp + "/data"
	defer func() { dataRoot = "/srv/truffels/data" }()
	_ = os.MkdirAll(dataRoot+"/cat-digibyted", 0o755)
	marker := dataRoot + "/cat-digibyted/keep.dat"
	_ = os.WriteFile(marker, []byte("x"), 0o644)

	rec := httptest.NewRecorder()
	body := `{"path":"` + dataRoot + `/cat-digibyted","uid":1000,"gid":1000,"mode":"0755"}`
	handleClearDir(rec, httptest.NewRequest(http.MethodPost, "/v1/fs/clear-dir", bytes.NewBufferString(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("clear-dir touched catalog data")
	}
}

func TestEnsureDirRejectsCatalogPath(t *testing.T) {
	tmp := t.TempDir()
	dataRoot = tmp + "/data"
	defer func() { dataRoot = "/srv/truffels/data" }()
	_ = os.MkdirAll(dataRoot, 0o755)

	rec := httptest.NewRecorder()
	body := `{"path":"` + dataRoot + `/cat-evil","uid":1000,"gid":1000,"mode":"0755"}`
	handleEnsureDir(rec, httptest.NewRequest(http.MethodPost, "/v1/fs/ensure-dir", bytes.NewBufferString(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(dataRoot + "/cat-evil"); !os.IsNotExist(err) {
		t.Fatal("ensure-dir created a catalog directory")
	}
}

func TestClearDirRejectsSymlinkIntoCatalog(t *testing.T) {
	tmp := t.TempDir()
	dataRoot = tmp + "/data"
	defer func() { dataRoot = "/srv/truffels/data" }()
	_ = os.MkdirAll(dataRoot+"/mempool", 0o755)
	_ = os.MkdirAll(dataRoot+"/cat-digibyted", 0o755)
	marker := dataRoot + "/cat-digibyted/keep.dat"
	_ = os.WriteFile(marker, []byte("x"), 0o644)

	// Create symlink: dataRoot/mempool/cache → ../cat-digibyted
	_ = os.Symlink("../cat-digibyted", dataRoot+"/mempool/cache")

	rec := httptest.NewRecorder()
	body := `{"path":"` + dataRoot + `/mempool/cache","uid":1000,"gid":1000,"mode":"0755"}`
	handleClearDir(rec, httptest.NewRequest(http.MethodPost, "/v1/fs/clear-dir", bytes.NewBufferString(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("clear-dir followed symlink into catalog directory")
	}
}

func TestFileReconcileRejectsSymlinkIntoCatalog(t *testing.T) {
	tmp := t.TempDir()
	composeRoot, configRoot = tmp+"/compose", tmp+"/config"
	defer func() {
		composeRoot, configRoot = "/srv/truffels/compose", "/srv/truffels/config"
	}()
	_ = os.MkdirAll(composeRoot+"/bitcoin", 0o755)
	_ = os.MkdirAll(composeRoot+"/cat-digibyted", 0o755)
	catFile := composeRoot + "/cat-digibyted/docker-compose.yml"
	_ = os.WriteFile(catFile, []byte("original"), 0o644)

	// Create symlink: composeRoot/bitcoin/docker-compose.yml → ../cat-digibyted/docker-compose.yml
	_ = os.Symlink("../cat-digibyted/docker-compose.yml", composeRoot+"/bitcoin/docker-compose.yml")

	rec := httptest.NewRecorder()
	body := `{"path":"bitcoin/docker-compose.yml","expected_content":"pwned"}`
	handleFileReconcile(rec, httptest.NewRequest(http.MethodPost, "/v1/file/reconcile", bytes.NewBufferString(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Code = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// Verify file was not modified
	data, _ := os.ReadFile(catFile)
	if string(data) != "original" {
		t.Fatal("file/reconcile followed symlink into catalog directory")
	}
}
