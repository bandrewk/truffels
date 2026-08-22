package main

import (
	"encoding/json"
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

// The pool config is valid JSON, points at the node by its stack DNS name,
// carries the shared credential, and includes the payout address/signature.
func TestRenderCkpoolDGBConf(t *testing.T) {
	creds := &stackCreds{User: "truffels", Pass: "secretpass"}
	files, err := RenderConfig("ckpool-dgb", map[string]any{
		"dgb_address": "dgb1qc4txtrd36vw0hrjz93gvfhd28dvv0uu37rphy6",
		"dgb_sig":     "/Truffels/",
	}, creds)
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	raw := files["ckpool.conf"]
	if raw == nil {
		t.Fatal("ckpool.conf missing")
	}
	var conf map[string]any
	if err := json.Unmarshal(raw, &conf); err != nil {
		t.Fatalf("ckpool.conf is not valid JSON: %v", err)
	}
	s := string(raw)
	for _, want := range []string{
		"truffels-digibyted-node:14022", "secretpass",
		"dgb1qc4txtrd36vw0hrjz93gvfhd28dvv0uu37rphy6", "0.0.0.0:3334", "/data/logs",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ckpool.conf missing %q:\n%s", want, s)
		}
	}
	if _, err := RenderConfig("ckpool-dgb", map[string]any{}, nil); err == nil {
		t.Error("ckpool-dgb without stack creds should error")
	}
}

// The payout address is required and pattern-checked.
func TestCkpoolDGBAddressValidation(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	e, ok := cat["ckpool-dgb"]
	if !ok {
		t.Fatal("ckpool-dgb not in catalog")
	}
	if _, err := ValidateParams(e, map[string]any{"dgb_address": "dgb1qc4txtrd36vw0hrjz93gvfhd28dvv0uu37rphy6"}); err != nil {
		t.Errorf("valid address rejected: %v", err)
	}
	if _, err := ValidateParams(e, map[string]any{}); err == nil {
		t.Error("missing dgb_address should be rejected (required)")
	}
	if _, err := ValidateParams(e, map[string]any{"dgb_address": "not an address!!"}); err == nil {
		t.Error("malformed dgb_address should be rejected")
	}
}

// The stats DB and app share the per-stack secret as their DB password, and the
// app points at the DB by its stack DNS name.
func TestRenderCkstatsEnvs(t *testing.T) {
	creds := &stackCreds{User: "truffels", Pass: "poolpass99"}
	db, err := RenderConfig("ckstats-db-dgb", map[string]any{}, creds)
	if err != nil {
		t.Fatalf("db env: %v", err)
	}
	if !strings.Contains(string(db["postgres.env"]), "POSTGRES_PASSWORD=poolpass99") {
		t.Errorf("postgres.env missing password: %s", db["postgres.env"])
	}
	app, err := RenderConfig("ckstats-dgb", map[string]any{}, creds)
	if err != nil {
		t.Fatalf("app env: %v", err)
	}
	s := string(app["ckstats.env"])
	for _, want := range []string{"DB_HOST=truffels-ckstats-db-dgb-db", "DB_PASSWORD=poolpass99", "API_URL=/ckpool-logs"} {
		if !strings.Contains(s, want) {
			t.Errorf("ckstats.env missing %q:\n%s", want, s)
		}
	}
	if _, err := RenderConfig("ckstats-db-dgb", map[string]any{}, nil); err == nil {
		t.Error("ckstats-db-dgb without creds should error")
	}
}

func TestRenderConfigBchn(t *testing.T) {
	files, err := RenderConfig("bchn", map[string]any{}, nil)
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	conf, ok := files["bch.conf"]
	if !ok {
		t.Fatal("bch.conf is missing")
	}
	s := string(conf)
	for _, want := range []string{"server=1", "disablewallet=1", "prune=5000", "listen=0", "dbcache=512", "zmqpubhashblock="} {
		if !strings.Contains(s, want) {
			t.Errorf("bch.conf missing %q:\n%s", want, s)
		}
	}
	// BCHN is not Bitcoin Core: rpcwhitelist does not exist there and sends the
	// node into a restart loop. It must never appear.
	if strings.Contains(s, "rpcwhitelist") {
		t.Error("rpcwhitelist must never be set — it does not exist in BCHN")
	}
	// Without stack creds there is no RPC password line.
	if strings.Contains(s, "rpcpassword=") {
		t.Error("bch.conf must not contain rpcpassword when rendered without creds")
	}
	// Guard against ever committing a payout address into the node config.
	if strings.Contains(s, "bitcoincash:") || strings.Contains(s, "addr") {
		t.Errorf("bch.conf must not contain an address:\n%s", s)
	}
}

func TestRenderConfigBchnStacked(t *testing.T) {
	creds := &stackCreds{User: "truffels", Pass: "deadbeefcafe"}
	files, err := RenderConfig("bchn", map[string]any{}, creds)
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	s := string(files["bch.conf"])
	for _, want := range []string{
		"rpcuser=truffels", "rpcpassword=deadbeefcafe",
		"rpcbind=0.0.0.0", "rpcallowip=172.16.0.0/12", "rpcport=8332",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stacked bch.conf missing %q:\n%s", want, s)
		}
	}
}
