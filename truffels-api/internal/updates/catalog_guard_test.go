package updates

import (
	"strings"
	"testing"
)

func TestIsCatalogServiceDetectsPrefix(t *testing.T) {
	setCatalogIDs(map[string]bool{"digibyted": true})
	defer setCatalogIDs(nil)

	if !IsCatalogService("digibyted") {
		t.Error("digibyted should be recognized as a catalog service")
	}
	for _, legacy := range []string{"bitcoind", "electrs", "ckpool", "mempool", "proxy"} {
		if IsCatalogService(legacy) {
			t.Errorf("%s was incorrectly recognized as a catalog service", legacy)
		}
	}
}

func TestApplyUpdateRefusesCatalogService(t *testing.T) {
	setCatalogIDs(map[string]bool{"digibyted": true})
	defer setCatalogIDs(nil)

	e := &Engine{}
	err := e.ApplyUpdate("digibyted")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "catalog") {
		t.Errorf("error message does not mention the reason: %v", err)
	}
}
