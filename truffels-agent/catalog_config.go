package main

import (
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
	default:
		return map[string][]byte{}, nil
	}
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
