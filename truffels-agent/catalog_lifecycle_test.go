package main

import "testing"

func TestServiceComposeDirAllowsCatalogAndLegacy(t *testing.T) {
	var err error
	loadedCatalog, err = LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	composeRoot = "/srv/truffels/compose"

	// legacy service
	if dir, ok := serviceComposeDir("bitcoind"); !ok || dir != "/srv/truffels/compose/bitcoin" {
		t.Errorf("legacy: dir=%q ok=%v", dir, ok)
	}
	// installed catalog service
	if dir, ok := serviceComposeDir("digibyted"); !ok || dir != "/srv/truffels/compose/cat-digibyted" {
		t.Errorf("catalog: dir=%q ok=%v", dir, ok)
	}
	// unknown
	if _, ok := serviceComposeDir("nope"); ok {
		t.Error("unknown service allowed")
	}
	// charset attack must not resolve
	if _, ok := serviceComposeDir("../bitcoin"); ok {
		t.Error("traversal id allowed")
	}
}

func TestIsAllowedContainerCoversCatalog(t *testing.T) {
	var err error
	loadedCatalog, err = LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if !isAllowedContainer("truffels-bitcoind") {
		t.Error("legacy container denied")
	}
	if !isAllowedContainer("truffels-digibyted-node") {
		t.Error("catalog container denied")
	}
	if isAllowedContainer("truffels-evil") {
		t.Error("unknown container allowed")
	}
}
