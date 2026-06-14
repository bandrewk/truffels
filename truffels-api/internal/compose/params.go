package compose

import (
	"fmt"
	"regexp"
)

// Parameter structs for each compose template.

type BitcoinParams struct {
	ImageTag string
}

type ElectrsParams struct {
	ImageTag string
}

type CkpoolParams struct {
	ImageTag string
}

type MempoolParams struct {
	BackendImageTag  string
	FrontendImageTag string
	DBImageTag       string
}

type CkstatsParams struct {
	CkstatsImageTag string
	DBImageTag      string
}

type ProxyParams struct {
	ImageTag string
}

type TruffelsParams struct {
	AgentTag string
	APITag   string
	WebTag   string
	// RepoSrc is the host path mounted as /repo on the agent container and used
	// as the build context for all three truffels images. Extracted from the
	// agent's "- <path>:/repo:rw" volume line on disk.
	RepoSrc string
}

// repoSrcRe extracts the host path from a line like
// "      - /home/truffel/Project-Truffels:/repo:rw"
var repoSrcRe = regexp.MustCompile(`(?m)^\s*-\s+(\S+):/repo:rw\s*$`)

// imageTagRe matches `image: <ref>` lines, capturing the full image reference.
var imageTagRe = regexp.MustCompile(`(?m)^\s*image:\s*(\S+)\s*$`)

// ExtractImageTag finds the image reference matching the given prefix in compose content.
// Returns the full image ref (e.g. "btcpayserver/bitcoin:30.2" or "mariadb:lts@sha256:abc...").
func ExtractImageTag(content, imagePrefix string) string {
	matches := imageTagRe.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		ref := m[1]
		// Check if ref starts with the prefix (before the colon/@ part)
		if len(ref) >= len(imagePrefix) && ref[:len(imagePrefix)] == imagePrefix {
			return ref
		}
	}
	return ""
}

// ExtractParams reads the current image tags from a compose file and returns
// the appropriate parameter struct for the given service.
func ExtractParams(serviceID, content string) (any, error) {
	switch serviceID {
	case "bitcoind":
		tag := ExtractImageTag(content, "btcpayserver/bitcoin:")
		if tag == "" {
			return nil, fmt.Errorf("no image tag found for bitcoind")
		}
		return BitcoinParams{ImageTag: tag}, nil

	case "electrs":
		tag := ExtractImageTag(content, "getumbrel/electrs:")
		if tag == "" {
			return nil, fmt.Errorf("no image tag found for electrs")
		}
		return ElectrsParams{ImageTag: tag}, nil

	case "ckpool":
		tag := ExtractImageTag(content, "truffels/ckpool:")
		if tag == "" {
			return nil, fmt.Errorf("no image tag found for ckpool")
		}
		return CkpoolParams{ImageTag: tag}, nil

	case "mempool":
		backend := ExtractImageTag(content, "mempool/backend:")
		frontend := ExtractImageTag(content, "mempool/frontend:")
		db := ExtractImageTag(content, "mariadb:")
		if backend == "" || frontend == "" || db == "" {
			return nil, fmt.Errorf("missing image tag(s) for mempool (backend=%q frontend=%q db=%q)", backend, frontend, db)
		}
		return MempoolParams{
			BackendImageTag:  backend,
			FrontendImageTag: frontend,
			DBImageTag:       db,
		}, nil

	case "ckstats":
		ckstats := ExtractImageTag(content, "truffels/ckstats:")
		db := ExtractImageTag(content, "postgres:")
		if ckstats == "" || db == "" {
			return nil, fmt.Errorf("missing image tag(s) for ckstats (ckstats=%q db=%q)", ckstats, db)
		}
		return CkstatsParams{
			CkstatsImageTag: ckstats,
			DBImageTag:      db,
		}, nil

	case "proxy":
		tag := ExtractImageTag(content, "caddy:")
		if tag == "" {
			return nil, fmt.Errorf("no image tag found for proxy")
		}
		return ProxyParams{ImageTag: tag}, nil

	case "truffels":
		agent := ExtractImageTag(content, "truffels/agent:")
		api := ExtractImageTag(content, "truffels/api:")
		web := ExtractImageTag(content, "truffels/web:")
		if agent == "" || api == "" || web == "" {
			return nil, fmt.Errorf("missing image tag(s) for truffels (agent=%q api=%q web=%q)", agent, api, web)
		}
		var repoSrc string
		if m := repoSrcRe.FindStringSubmatch(content); m != nil {
			repoSrc = m[1]
		}
		if repoSrc == "" {
			return nil, fmt.Errorf("missing /repo:rw mount for truffels (extract RepoSrc)")
		}
		return TruffelsParams{
			AgentTag: agent,
			APITag:   api,
			WebTag:   web,
			RepoSrc:  repoSrc,
		}, nil

	default:
		return nil, fmt.Errorf("unknown service for param extraction: %q", serviceID)
	}
}
