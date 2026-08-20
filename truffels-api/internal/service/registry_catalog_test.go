package service_test

import (
	"testing"
	"truffels-api/internal/catalog"
	"truffels-api/internal/service"
	"truffels-api/internal/store"
)

type fakeCatalogSource struct {
	entries map[string]catalog.Entry
	err     error
}

func (f *fakeCatalogSource) All() (map[string]catalog.Entry, error) {
	return f.entries, f.err
}

type fakeInstallSource struct {
	installations []store.CatalogInstallation
	err           error
}

func (f *fakeInstallSource) ListCatalogInstallations() ([]store.CatalogInstallation, error) {
	return f.installations, f.err
}

func TestRegistry_Refresh(t *testing.T) {
	catSource := &fakeCatalogSource{
		entries: map[string]catalog.Entry{
			"digibyted": {
				ID:                  "digibyted",
				DisplayName:         "DigiByte Core",
				ContainerNamesField: []string{"digibyted"},
			},
		},
	}
	installSource := &fakeInstallSource{
		installations: []store.CatalogInstallation{
			{CatalogID: "digibyted"},
		},
	}

	r := service.NewRegistry("/compose", "/data", "repo", catSource, installSource)
	err := r.Refresh()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// 1. Should have legacy service (e.g., bitcoind)
	_, ok := r.Get("bitcoind")
	if !ok {
		t.Errorf("expected legacy service 'bitcoind' to be present")
	}

	// 2. Should have projected catalog service
	svc, ok := r.Get("digibyted")
	if !ok {
		t.Fatalf("expected catalog service 'digibyted' to be present")
	}

	if svc.ID != "digibyted" {
		t.Errorf("expected ID 'digibyted', got %q", svc.ID)
	}
	if svc.ComposeDir != "/compose/cat-digibyted" {
		t.Errorf("expected ComposeDir '/compose/cat-digibyted', got %q", svc.ComposeDir)
	}

	// 3. Verify order contains legacy then catalog
	all := r.All()
	var foundBitcoind, foundDigibyte int
	for i, s := range all {
		if s.ID == "bitcoind" {
			foundBitcoind = i
		}
		if s.ID == "digibyted" {
			foundDigibyte = i
		}
	}
	if foundBitcoind >= foundDigibyte {
		t.Errorf("expected bitcoind before digibyted in order, bitcoind=%d, digibyted=%d", foundBitcoind, foundDigibyte)
	}

	// 4. Test empty installation case
	installSource.installations = nil
	err = r.Refresh()
	if err != nil {
		t.Fatalf("expected no error on empty install, got %v", err)
	}

	_, ok = r.Get("digibyted")
	if ok {
		t.Errorf("expected 'digibyted' to be removed after refresh with empty installations")
	}
}
