package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func catNetworkName(id string) string { return "cat-" + id + "-net" }

func RenderCompose(e CatalogEntry, params map[string]any) ([]byte, error) {
	if !isValidCatalogID(e.ID) {
		return nil, fmt.Errorf("invalid id %q", e.ID)
	}
	image, err := catalogImageRef(e)
	if err != nil {
		return nil, err
	}

	cf := composeFile{
		Name:     "cat-" + e.ID,
		Services: make(map[string]composeService, len(e.Containers)),
		Networks: map[string]composeNetwork{
			"chain": {Name: catNetworkName(e.ID)},
		},
	}

	for _, c := range e.Containers {
		svc := composeService{
			Image:         image,
			ContainerName: catContainerName(e.ID, c.Name),
			Restart:       "unless-stopped",
			User:          c.User,
			SecurityOpt:   []string{"no-new-privileges:true"},
			CapDrop:       []string{"ALL"},
			Networks:      []string{"chain"},
			Entrypoint:    c.Entrypoint,
			Deploy: composeDeploy{Resources: composeResources{
				Limits: composeLimits{Memory: strconv.Itoa(c.MemoryLimitMB) + "M"},
			}},
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
			if v.RO {
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

func catalogImageRef(e CatalogEntry) (string, error) {
	if e.Image != "" {
		return e.Image, nil
	}
	if e.Build != nil {
		return "truffels/" + e.ID + ":" + e.Build.Version, nil
	}
	return "", fmt.Errorf("%s: neither image nor build specified", e.ID)
}
