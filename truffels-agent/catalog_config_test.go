package main

import (
	"strings"
	"testing"
)

func TestRenderConfigDigibyted(t *testing.T) {
	files, err := RenderConfig("digibyted", map[string]any{}, nil)
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
	// Unstacked: no RPC exposure.
	if strings.Contains(s, "rpcbind") || strings.Contains(s, "rpcuser") {
		t.Errorf("unstacked node must not expose RPC:\n%s", s)
	}
}

// When stacked, the node exposes RPC with the shared credential so the pool
// can reach it.
func TestRenderConfigDigibytedStacked(t *testing.T) {
	creds := &stackCreds{User: "truffels", Pass: "deadbeefcafe"}
	files, err := RenderConfig("digibyted", map[string]any{}, creds)
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	s := string(files["digibyte.conf"])
	for _, want := range []string{
		"rpcuser=truffels", "rpcpassword=deadbeefcafe",
		"rpcbind=0.0.0.0", "rpcallowip=172.16.0.0/12", "rpcport=14022",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stacked digibyte.conf missing %q:\n%s", want, s)
		}
	}
}
