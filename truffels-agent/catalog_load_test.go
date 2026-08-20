package main

import "testing"

func TestLoadCatalogParsesDigibyted(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	e, ok := cat["digibyted"]
	if !ok {
		t.Fatal("digibyted missing from catalog")
	}
	if e.Role != "chain-node" {
		t.Errorf("Role = %q, expected chain-node", e.Role)
	}
	if e.Implementation != "digibyte-core" {
		t.Errorf("Implementation = %q, expected digibyte-core", e.Implementation)
	}
	if e.Chain != "dgb" {
		t.Errorf("Chain = %q, expected dgb", e.Chain)
	}
	if len(e.Containers) != 1 || e.Containers[0].Name != "node" {
		t.Fatalf("expected exactly one container 'node', got %+v", e.Containers)
	}
}

func TestLoadCatalogRejectsUnknownField(t *testing.T) {
	raw := []byte(`{"schema_version":1,"id":"x","display_name":"X","description":"d",
	  "role":"chain-node","implementation":"y","containers":[],"resources":{"memory_floor_mb":1},
	  "bogus_field":true}`)
	if _, err := parseEntry(raw); err == nil {
		t.Fatal("expected error on unknown field, got nil")
	}
}

func TestLoadCatalogRejectsTrailingData(t *testing.T) {
	// Two valid JSON objects concatenated — second one should trigger an error
	raw := []byte(`{"schema_version":1,"id":"x","display_name":"X","description":"d",
	  "role":"chain-node","implementation":"y","containers":[],"resources":{"memory_floor_mb":1}}
	{"schema_version":1,"id":"y","display_name":"Y","description":"d",
	  "role":"chain-node","implementation":"z","containers":[],"resources":{"memory_floor_mb":1}}`)
	if _, err := parseEntry(raw); err == nil {
		t.Fatal("expected error on trailing data, got nil")
	}
}
