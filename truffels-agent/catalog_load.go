package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
)

// Embeds all catalog JSON files at compile time.
//
//go:embed catalog/*.json
var catalogFS embed.FS

type Catalog map[string]CatalogEntry

// parseEntry decodes strictly: an unknown field is an error, not silent
// ignorance. A typo in a catalog entry should fail at startup, not in
// production. Also rejects trailing data to catch accidental concatenation.
func parseEntry(raw []byte) (CatalogEntry, error) {
	var e CatalogEntry
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return CatalogEntry{}, err
	}
	if dec.More() {
		return CatalogEntry{}, fmt.Errorf("trailing data after catalog entry")
	}
	return e, nil
}

// validateEntry checks fields that parseEntry cannot verify:
// container names, empty containers, memory limits, and image references.
func validateEntry(e CatalogEntry) error {
	if len(e.Containers) == 0 {
		return fmt.Errorf("entry %q: containers must not be empty", e.ID)
	}
	for _, c := range e.Containers {
		// Container names use the same rule as catalog IDs — intentional.
		if !catalogIDRe.MatchString(c.Name) {
			return fmt.Errorf("entry %q: invalid container name %q", e.ID, c.Name)
		}
		if c.MemoryLimitMB <= 0 {
			return fmt.Errorf("entry %q: container %q: memory_limit_mb must be > 0", e.ID, c.Name)
		}
	}
	if e.Image != "" {
		// managedImageRefChars is defined in allowlists.go (same package).
		if !hasOnlyChars(e.Image, managedImageRefChars) {
			return fmt.Errorf("entry %q: image %q contains invalid characters", e.ID, e.Image)
		}
	}
	if e.Build != nil && e.Build.Version == "" {
		return fmt.Errorf("entry %q: build is set but version is empty", e.ID)
	}
	return nil
}

func LoadCatalog() (Catalog, error) {
	entries, err := fs.ReadDir(catalogFS, "catalog")
	if err != nil {
		return nil, fmt.Errorf("catalog dir: %w", err)
	}
	cat := make(Catalog, len(entries))
	for _, de := range entries {
		raw, err := catalogFS.ReadFile(path.Join("catalog", de.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", de.Name(), err)
		}
		e, err := parseEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", de.Name(), err)
		}
		if e.SchemaVersion != 1 {
			return nil, fmt.Errorf("%s: schema_version %d not supported", de.Name(), e.SchemaVersion)
		}
		if !isValidCatalogID(e.ID) {
			return nil, fmt.Errorf("%s: invalid id %q", de.Name(), e.ID)
		}
		if _, dup := cat[e.ID]; dup {
			return nil, fmt.Errorf("duplicate id %q", e.ID)
		}
		if err := validateEntry(e); err != nil {
			return nil, fmt.Errorf("%s: %w", de.Name(), err)
		}
		cat[e.ID] = e
	}
	return cat, nil
}

