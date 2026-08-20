package main

import (
	"strings"
	"testing"
)

func TestRenderConfigDigibyted(t *testing.T) {
	files, err := RenderConfig("digibyted", map[string]any{"prune_gb": 0})
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	conf, ok := files["digibyte.conf"]
	if !ok {
		t.Fatal("digibyte.conf is missing")
	}
	s := string(conf)
	// The hard precondition from Phase 0: without algo=sha256d,
	// getblocktemplate returns a Scrypt template.
	if !strings.Contains(s, "algo=sha256d") {
		t.Error("algo=sha256d is missing in digibyte.conf")
	}
	if strings.Contains(s, "prune=") {
		t.Error("prune must not be set when prune_gb=0")
	}
}

func TestRenderConfigSetsPrune(t *testing.T) {
	files, _ := RenderConfig("digibyted", map[string]any{"prune_gb": 12})
	if !strings.Contains(string(files["digibyte.conf"]), "prune=12288") {
		t.Errorf("prune=12288 is missing, got:\n%s", files["digibyte.conf"])
	}
}
