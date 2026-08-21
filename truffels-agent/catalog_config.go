package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// RenderConfig builds line-based config files. Deliberately no text/template:
// the values have already passed through ValidateParams and been checked for
// control characters there, and the structure stays readable without any
// escaping concerns.
//
// For further entries the switch grows. This is intentional — the config
// formats of the services are too different for a common abstraction, and a
// generic renderer would be exactly the place where an escaping problem
// re-emerges.
func RenderConfig(id string, params map[string]any, creds *stackCreds) (map[string][]byte, error) {
	switch id {
	case "digibyted":
		return renderDigibyteConf(creds)
	case "ckpool-dgb":
		return renderCkpoolDGBConf(params, creds)
	case "ckstats-db-dgb":
		return renderCkstatsDBEnv(creds)
	case "ckstats-dgb":
		return renderCkstatsEnv(creds)
	default:
		return map[string][]byte{}, nil
	}
}

// renderCkstatsDBEnv writes the postgres env for the stats database. The
// password is the shared per-stack secret, so the stats app authenticates with
// the same value.
func renderCkstatsDBEnv(creds *stackCreds) (map[string][]byte, error) {
	if creds == nil {
		return nil, fmt.Errorf("ckstats-db-dgb requires stack credentials")
	}
	env := "POSTGRES_USER=ckpool\nPOSTGRES_DB=ckstats\nPOSTGRES_PASSWORD=" + creds.Pass + "\n"
	return map[string][]byte{"postgres.env": []byte(env)}, nil
}

// renderCkstatsEnv writes the ckstats app env: where to read the pool logs, and
// how to reach its database (by the db's stack DNS name, with the shared secret).
func renderCkstatsEnv(creds *stackCreds) (map[string][]byte, error) {
	if creds == nil {
		return nil, fmt.Errorf("ckstats-dgb requires stack credentials")
	}
	var b strings.Builder
	b.WriteString("API_URL=/ckpool-logs\n")
	b.WriteString("DB_HOST=" + catContainerName("ckstats-db-dgb", "db") + "\n")
	b.WriteString("DB_PORT=5432\n")
	b.WriteString("DB_USER=ckpool\n")
	b.WriteString("DB_PASSWORD=" + creds.Pass + "\n")
	b.WriteString("DB_NAME=ckstats\n")
	b.WriteString("DB_SSL=false\n")
	b.WriteString("DB_SSL_REJECT_UNAUTHORIZED=false\n")
	return map[string][]byte{"ckstats.env": []byte(b.String())}, nil
}

// ckpool.conf is JSON, and two of its values (address, signature) are user
// params. Building it with encoding/json means a param can never break out of
// its string — the same structural-safety argument the compose generator makes.
type ckpoolBtcd struct {
	URL    string `json:"url"`
	Auth   string `json:"auth"`
	Pass   string `json:"pass"`
	Notify bool   `json:"notify"`
}

type ckpoolConf struct {
	Btcd      []ckpoolBtcd `json:"btcd"`
	ZmqBlock  string       `json:"zmqblock"`
	BlockPoll int          `json:"blockpoll"`
	LogDir    string       `json:"logdir"`
	BtcAddr   string       `json:"btcaddress"`
	BtcSig    string       `json:"btcsig"`
	ServerURL []string     `json:"serverurl"`
	StartDiff int          `json:"startdiff"`
	MinDiff   int          `json:"mindiff"`
	MaxDiff   int          `json:"maxdiff"`
}

func renderCkpoolDGBConf(params map[string]any, creds *stackCreds) (map[string][]byte, error) {
	if creds == nil {
		return nil, fmt.Errorf("ckpool-dgb requires stack credentials")
	}
	addr, _ := params["dgb_address"].(string)
	sig, _ := params["dgb_sig"].(string)
	// The node's container name resolves on the shared stack network.
	node := catContainerName("digibyted", "node")
	conf := ckpoolConf{
		Btcd:      []ckpoolBtcd{{URL: node + ":14022", Auth: creds.User, Pass: creds.Pass, Notify: true}},
		ZmqBlock:  "tcp://" + node + ":28332",
		BlockPoll: 100,
		LogDir:    "/data/logs",
		BtcAddr:   addr,
		BtcSig:    sig,
		ServerURL: []string{"0.0.0.0:3334"},
		StartDiff: 1,
		MinDiff:   1,
		MaxDiff:   0,
	}
	b, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"ckpool.conf": b}, nil
}

func renderDigibyteConf(creds *stackCreds) (map[string][]byte, error) {
	var b strings.Builder
	b.WriteString("# Project Truffels — DigiByte Core\n")
	b.WriteString("# Generated from the catalog. Do not edit by hand.\n")
	b.WriteString("server=1\n")
	b.WriteString("printtoconsole=1\n")
	b.WriteString("disablewallet=1\n")
	// This build ships DigiDollar compiled in, which refuses to start unless
	// txindex=1. That also rules out pruning (prune and txindex are mutually
	// exclusive), so this entry runs as a full node with no prune option.
	b.WriteString("txindex=1\n")
	// Without this line getblocktemplate returns a Scrypt template:
	// src/init.cpp:198 sets miningAlgo = ALGO_SCRYPT, and ckpool does not
	// send an Algo argument. The failure would be silent — the RPC response
	// is valid, just worthless for a SHA256d pool. See Spec 2.4.
	b.WriteString("algo=sha256d\n")
	b.WriteString("zmqpubhashblock=tcp://0.0.0.0:28332\n")

	// When part of a stack, expose RPC so the pool can reach the node with the
	// shared credential. rpcallowip uses the broad private ranges the legacy
	// node uses; actual reachability stays scoped to the shared stack network.
	// The password is hex from crypto/rand — no config-escaping concern.
	if creds != nil {
		b.WriteString("rpcuser=" + creds.User + "\n")
		b.WriteString("rpcpassword=" + creds.Pass + "\n")
		b.WriteString("rpcbind=0.0.0.0\n")
		b.WriteString("rpcallowip=172.16.0.0/12\n")
		b.WriteString("rpcallowip=10.0.0.0/8\n")
		b.WriteString("rpcport=14022\n")
	}

	return map[string][]byte{"digibyte.conf": []byte(b.String())}, nil
}

// safeConfigKey validates that a config file name is a safe bare filename:
// not empty, equal to its own filepath.Base (no path separators), not "." or
// "..", and containing no control characters.
func safeConfigKey(name string) error {
	if name == "" {
		return fmt.Errorf("config key must not be empty")
	}
	if name != filepath.Base(name) {
		return fmt.Errorf("config key %q must be a bare filename", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("config key %q is not allowed", name)
	}
	if err := rejectsControlChars(name); err != nil {
		return fmt.Errorf("config key %q: %w", name, err)
	}
	return nil
}
