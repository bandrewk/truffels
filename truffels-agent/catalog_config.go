package main

import (
	"fmt"
	"strconv"
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
func RenderConfig(id string, params map[string]any) (map[string][]byte, error) {
	switch id {
	case "digibyted":
		return renderDigibyteConf(params)
	default:
		return map[string][]byte{}, nil
	}
}

func renderDigibyteConf(params map[string]any) (map[string][]byte, error) {
	pruneGB, ok := params["prune_gb"].(int)
	if !ok {
		return nil, fmt.Errorf("prune_gb is missing or has the wrong type: %T", params["prune_gb"])
	}

	var b strings.Builder
	b.WriteString("# Project Truffels — DigiByte Core\n")
	b.WriteString("# Generated from the catalog. Do not edit by hand.\n")
	b.WriteString("server=1\n")
	b.WriteString("printtoconsole=1\n")
	b.WriteString("disablewallet=1\n")
	b.WriteString("txindex=0\n")
	// Without this line getblocktemplate returns a Scrypt template:
	// src/init.cpp:198 sets miningAlgo = ALGO_SCRYPT, and ckpool does not
	// send an Algo argument. The failure would be silent — the RPC response
	// is valid, just worthless for a SHA256d pool. See Spec 2.4.
	b.WriteString("algo=sha256d\n")
	if pruneGB > 0 {
		b.WriteString("prune=" + strconv.Itoa(pruneGB*1024) + "\n")
	}
	b.WriteString("zmqpubhashblock=tcp://0.0.0.0:28332\n")

	return map[string][]byte{"digibyte.conf": []byte(b.String())}, nil
}
