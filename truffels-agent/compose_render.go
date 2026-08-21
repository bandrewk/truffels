package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func catNetworkName(id string) string { return "cat-" + id + "-net" }

// stackNetworkName is the shared external network every member of a stack joins.
func stackNetworkName(stack string) string { return "cat-stack-" + stack + "-net" }

func RenderCompose(e CatalogEntry, params map[string]any) ([]byte, error) {
	if !isValidCatalogID(e.ID) {
		return nil, fmt.Errorf("invalid id %q", e.ID)
	}
	image, err := catalogImageRef(e)
	if err != nil {
		return nil, err
	}

	// A build entry ships a Dockerfile in the repo instead of a pre-built image.
	// The block is shared by every container of the entry (they run the same
	// image); compose/buildkit dedups the build by image tag.
	var build *composeBuild
	if e.Build != nil {
		build, err = catalogBuildBlock(e.Build)
		if err != nil {
			return nil, err
		}
	}

	cf := composeFile{
		Name:     "cat-" + e.ID,
		Services: make(map[string]composeService, len(e.Containers)),
		Networks: map[string]composeNetwork{
			"chain": {Name: catNetworkName(e.ID)},
		},
	}

	// A stacked entry additionally joins the shared external stack network, so
	// its containers can reach the other stack members by name. The network is
	// created and torn down by the agent, not compose (External: true).
	svcNetworks := []string{"chain"}
	if e.Stack != "" {
		if !isValidCatalogID(e.Stack) {
			return nil, fmt.Errorf("invalid stack %q", e.Stack)
		}
		cf.Networks["stack"] = composeNetwork{Name: stackNetworkName(e.Stack), External: true}
		svcNetworks = []string{"chain", "stack"}
	}

	for _, c := range e.Containers {
		svc := composeService{
			Image:         image,
			ContainerName: catContainerName(e.ID, c.Name),
			Restart:       "unless-stopped",
			User:          c.User,
			SecurityOpt:   []string{"no-new-privileges:true"},
			CapDrop:       []string{"ALL"},
			Networks:      svcNetworks,
			Entrypoint:    c.Entrypoint,
			Deploy: composeDeploy{Resources: composeResources{
				Limits: composeLimits{Memory: strconv.Itoa(c.MemoryLimitMB) + "M"},
			}},
			Build: build,
		}
		for _, p := range c.Ports {
			svc.Ports = append(svc.Ports,
				strconv.Itoa(p.Host)+":"+strconv.Itoa(p.Container))
		}
		for _, v := range c.Volumes {
			src, err := volumeSource(e.ID, v)
			if err != nil {
				return nil, err
			}
			line := src + ":" + v.Mount
			// Borrowed pool logs are always read-only: a stats service must
			// never write into the pool's data.
			if v.RO || v.Kind == "pool-logs" {
				line += ":ro"
			}
			svc.Volumes = append(svc.Volumes, line)
		}
		if c.Healthcheck != nil {
			svc.Healthcheck = &composeHealth{
				Test:        c.Healthcheck.Test,
				Interval:    c.Healthcheck.Interval,
				Timeout:     c.Healthcheck.Timeout,
				Retries:     c.Healthcheck.Retries,
				StartPeriod: c.Healthcheck.StartPeriod,
			}
		}
		cf.Services[c.Name] = svc
	}

	return json.MarshalIndent(cf, "", "  ")
}

// volumeSource derives the host path from kind. The catalog never names a
// host path, so an entry cannot mount "/".
func volumeSource(id string, v VolumeSpec) (string, error) {
	// Validate mount path before using it: the short volume syntax splits on ':',
	// so the mount path itself must never carry one.
	if err := validateMountPath(v.Mount); err != nil {
		return "", err
	}

	switch v.Kind {
	case "data":
		return catDataDir(id), nil
	case "pool-logs":
		// A stats service borrows the pool's log directory. From is another
		// catalog entry's id (validated), so the path is derived, never passed.
		if !isValidCatalogID(v.From) {
			return "", fmt.Errorf("pool-logs volume needs a valid from, got %q", v.From)
		}
		return catDataDir(v.From) + "/logs", nil
	case "config":
		if v.File == "" {
			return "", fmt.Errorf("volume kind=config without file")
		}
		if err := rejectsControlChars(v.File); err != nil {
			return "", fmt.Errorf("volume file: %w", err)
		}
		// By contract, file is a bare filename. Any path separator or dot
		// segment would let a catalog entry escape the derived config root.
		if v.File != filepath.Base(v.File) || v.File == "." || v.File == ".." {
			return "", fmt.Errorf("volume file must be a bare filename, got %q", v.File)
		}
		return catConfigPath(id, v.File), nil
	default:
		return "", fmt.Errorf("unknown volume kind %q", v.Kind)
	}
}

// validateMountPath checks that a mount path is valid for the short volume syntax.
// The path must be absolute and clean, and cannot contain ':' (which splits the syntax).
func validateMountPath(mount string) error {
	if !filepath.IsAbs(mount) {
		return fmt.Errorf("volume mount must be an absolute path, got %q", mount)
	}
	if filepath.Clean(mount) != mount {
		return fmt.Errorf("volume mount must be a clean path, got %q", mount)
	}
	if strings.Contains(mount, ":") {
		return fmt.Errorf("volume mount must be an absolute clean path without ':'")
	}
	return nil
}

// repoMount is where the product repository is bind-mounted inside the agent
// container. Catalog build contexts resolve against this path because the `up`
// path runs `docker compose` in the agent's own namespace (unlike the legacy
// build path, which uses nsenter and host paths).
const repoMount = "/repo"

// catalogBuildBlock turns a catalog BuildSpec into a compose build block whose
// context is confined to the read-only /repo mount. It rejects any dockerfile
// path that is empty, absolute, unclean, or escapes the repo root, so a catalog
// entry can never point the build at an arbitrary host location.
func catalogBuildBlock(b *BuildSpec) (*composeBuild, error) {
	df := b.Dockerfile
	if df == "" {
		return nil, fmt.Errorf("build entry without dockerfile")
	}
	if err := rejectsControlChars(df); err != nil {
		return nil, fmt.Errorf("build dockerfile: %w", err)
	}
	if filepath.IsAbs(df) || filepath.Clean(df) != df {
		return nil, fmt.Errorf("build dockerfile must be a clean relative path, got %q", df)
	}
	if df == ".." || strings.HasPrefix(df, "../") {
		return nil, fmt.Errorf("build dockerfile escapes repo, got %q", df)
	}
	return &composeBuild{
		Context:    filepath.Join(repoMount, filepath.Dir(df)),
		Dockerfile: filepath.Base(df),
	}, nil
}

func catalogImageRef(e CatalogEntry) (string, error) {
	if e.Image != "" {
		return e.Image, nil
	}
	if e.Build != nil {
		return "truffels/" + e.ID + ":" + e.Build.Version, nil
	}
	return "", fmt.Errorf("%s: neither image nor build specified", e.ID)
}
