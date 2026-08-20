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

func TestValidateEntryRejectsEmptyContainers(t *testing.T) {
	raw := []byte(`{"schema_version":1,"id":"x","display_name":"X","description":"d",
	  "role":"chain-node","implementation":"y","containers":[],"resources":{"memory_floor_mb":1}}`)
	e, err := parseEntry(raw)
	if err != nil {
		t.Fatalf("parseEntry: %v", err)
	}
	if err := validateEntry(e); err == nil {
		t.Fatal("expected error for empty containers, got nil")
	}
}

func TestValidateEntryRejectsInvalidContainerName(t *testing.T) {
	raw := []byte(`{"schema_version":1,"id":"x","display_name":"X","description":"d",
	  "role":"chain-node","implementation":"y","containers":[{"name":"Node!","memory_limit_mb":100}],
	  "resources":{"memory_floor_mb":1}}`)
	e, err := parseEntry(raw)
	if err != nil {
		t.Fatalf("parseEntry: %v", err)
	}
	if err := validateEntry(e); err == nil {
		t.Fatal("expected error for invalid container name 'Node!', got nil")
	}

	raw2 := []byte(`{"schema_version":1,"id":"x","display_name":"X","description":"d",
	  "role":"chain-node","implementation":"y","containers":[{"name":"","memory_limit_mb":100}],
	  "resources":{"memory_floor_mb":1}}`)
	e2, err := parseEntry(raw2)
	if err != nil {
		t.Fatalf("parseEntry: %v", err)
	}
	if err := validateEntry(e2); err == nil {
		t.Fatal("expected error for empty container name, got nil")
	}
}

func TestValidateEntryRejectsZeroMemoryLimit(t *testing.T) {
	raw := []byte(`{"schema_version":1,"id":"x","display_name":"X","description":"d",
	  "role":"chain-node","implementation":"y","containers":[{"name":"node","memory_limit_mb":0}],
	  "resources":{"memory_floor_mb":1}}`)
	e, err := parseEntry(raw)
	if err != nil {
		t.Fatalf("parseEntry: %v", err)
	}
	if err := validateEntry(e); err == nil {
		t.Fatal("expected error for memory_limit_mb <= 0, got nil")
	}
}

func TestValidateEntryRejectsInvalidImageChars(t *testing.T) {
	raw := []byte(`{"schema_version":1,"id":"x","display_name":"X","description":"d",
	  "role":"chain-node","implementation":"y","containers":[{"name":"node","memory_limit_mb":100}],
	  "image":"bad/image!name","resources":{"memory_floor_mb":1}}`)
	e, err := parseEntry(raw)
	if err != nil {
		t.Fatalf("parseEntry: %v", err)
	}
	if err := validateEntry(e); err == nil {
		t.Fatal("expected error for invalid image characters, got nil")
	}
}

func TestValidateEntryRejectsBuildWithoutVersion(t *testing.T) {
	raw := []byte(`{"schema_version":1,"id":"x","display_name":"X","description":"d",
	  "role":"chain-node","implementation":"y","containers":[{"name":"node","memory_limit_mb":100}],
	  "build":{"version":""},"resources":{"memory_floor_mb":1}}`)
	e, err := parseEntry(raw)
	if err != nil {
		t.Fatalf("parseEntry: %v", err)
	}
	if err := validateEntry(e); err == nil {
		t.Fatal("expected error for build with empty version, got nil")
	}
}

func TestValidateEntryAcceptsValidDigibyted(t *testing.T) {
	raw := []byte(`{"schema_version":1,"id":"digibyted","display_name":"Digibyte","description":"d",
	  "role":"chain-node","implementation":"digibyte-core","chain":"dgb",
	  "containers":[{"name":"node","memory_limit_mb":512}],
	  "image":"registry.example.com/digibyte:1.0",
	  "resources":{"memory_floor_mb":256}}`)
	e, err := parseEntry(raw)
	if err != nil {
		t.Fatalf("parseEntry: %v", err)
	}
	if err := validateEntry(e); err != nil {
		t.Fatalf("expected valid entry to pass, got: %v", err)
	}
}
