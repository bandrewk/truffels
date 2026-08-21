package main

import (
	"strings"
	"testing"
)

func TestRenderConfigDigibyted(t *testing.T) {
	files, err := RenderConfig("digibyted", map[string]any{})
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
	// This build's DigiDollar refuses to start without txindex=1.
	if !strings.Contains(s, "txindex=1") {
		t.Error("txindex=1 is missing in digibyte.conf")
	}
	// txindex=1 rules out pruning: the entry is a full node, never pruned.
	if strings.Contains(s, "prune=") {
		t.Error("prune must never be set (incompatible with txindex=1)")
	}
}
