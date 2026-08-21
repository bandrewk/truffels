package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderComposeShape(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	e := cat["digibyted"]
	params, err := ValidateParams(e, map[string]any{})
	if err != nil {
		t.Fatalf("ValidateParams: %v", err)
	}
	out, err := RenderCompose(e, params)
	if err != nil {
		t.Fatalf("RenderCompose: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("Output is not valid JSON: %v", err)
	}
	svcs := doc["services"].(map[string]any)
	node := svcs["node"].(map[string]any)
	if node["container_name"] != "truffels-digibyted-node" {
		t.Errorf("container_name = %v", node["container_name"])
	}
	if got := node["security_opt"].([]any)[0]; got != "no-new-privileges:true" {
		t.Errorf("security_opt = %v", got)
	}
}

// The forbidden keys must not appear in ANY output, for no catalog entry.
// See Spec 4.1.
func TestRenderComposeNeverEmitsForbiddenKeys(t *testing.T) {
	forbidden := []string{
		"privileged", "cap_add", "\"pid\"", "\"ipc\"", "network_mode",
		"userns_mode", "devices", "docker.sock",
	}
	cat, _ := LoadCatalog()
	for id, e := range cat {
		params, err := ValidateParams(e, map[string]any{})
		if err != nil {
			t.Fatalf("%s: ValidateParams: %v", id, err)
		}
		out, err := RenderCompose(e, params)
		if err != nil {
			t.Fatalf("%s: RenderCompose: %v", id, err)
		}
		for _, k := range forbidden {
			if strings.Contains(string(out), k) {
				t.Errorf("%s: forbidden key %q in the output", id, k)
			}
		}
	}
}

// All bind-mount sources must lie under the derived roots.
func TestRenderComposeVolumesStayUnderDerivedRoots(t *testing.T) {
	cat, _ := LoadCatalog()
	for id, e := range cat {
		params, _ := ValidateParams(e, map[string]any{})
		out, _ := RenderCompose(e, params)
		var doc map[string]any
		_ = json.Unmarshal(out, &doc)
		for _, sv := range doc["services"].(map[string]any) {
			vols, ok := sv.(map[string]any)["volumes"].([]any)
			if !ok {
				continue
			}
			for _, v := range vols {
				src := strings.SplitN(v.(string), ":", 2)[0]
				src = filepath.Clean(src)
				okPrefix := strings.HasPrefix(src, catDataDir(id)) ||
					strings.HasPrefix(src, configRoot+"/cat-"+id+"/")
				if !okPrefix {
					t.Errorf("%s: bind source %q lies outside the derived roots", id, src)
				}
			}
		}
	}
}

// A catalog entry must not be able to escape the derived config root via
// the file field — the renderer refuses anything but a bare filename.
func TestRenderComposeRejectsTraversalFile(t *testing.T) {
	e := CatalogEntry{
		SchemaVersion: 1, ID: "evil", DisplayName: "Evil", Description: "d",
		Role: "chain-node", Implementation: "x", Image: "busybox:1",
		Containers: []ContainerSpec{{
			Name: "c", MemoryLimitMB: 64,
			Volumes: []VolumeSpec{{Kind: "config", File: "../evil.conf", Mount: "/x", RO: true}},
		}},
		Resources: ResourceSpec{MemoryFloorMB: 64},
	}
	if _, err := RenderCompose(e, map[string]any{}); err == nil {
		t.Fatal("expected error for traversal in volume file, got nil")
	}
}

// Mount paths containing ':' break the short volume syntax and must be rejected.
func TestRenderComposeRejectsInvalidMountPath(t *testing.T) {
	e := CatalogEntry{
		SchemaVersion: 1, ID: "evil", DisplayName: "Evil", Description: "d",
		Role: "chain-node", Implementation: "x", Image: "busybox:1",
		Containers: []ContainerSpec{{
			Name: "c", MemoryLimitMB: 64,
			Volumes: []VolumeSpec{{Kind: "data", Mount: "/data:rw", RO: false}},
		}},
		Resources: ResourceSpec{MemoryFloorMB: 64},
	}
	if _, err := RenderCompose(e, map[string]any{}); err == nil {
		t.Fatal("expected error for mount path containing ':', got nil")
	}
}

// A build entry (digibyted ships a Dockerfile, not a pre-built image) must emit
// a build block whose context is confined to the read-only /repo mount, so
// `docker compose up` builds the image on first start.
func TestRenderComposeBuildEntry(t *testing.T) {
	cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	e := cat["digibyted"]
	if e.Build == nil {
		t.Fatal("expected digibyted to be a build entry")
	}
	params, err := ValidateParams(e, map[string]any{})
	if err != nil {
		t.Fatalf("ValidateParams: %v", err)
	}
	out, err := RenderCompose(e, params)
	if err != nil {
		t.Fatalf("RenderCompose: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("Output is not valid JSON: %v", err)
	}
	node := doc["services"].(map[string]any)["node"].(map[string]any)
	build, ok := node["build"].(map[string]any)
	if !ok {
		t.Fatalf("build block missing on build entry: %v", node["build"])
	}
	if build["context"] != "/repo/dockerfiles/digibyted" {
		t.Errorf("context = %v, want /repo/dockerfiles/digibyted", build["context"])
	}
	if build["dockerfile"] != "Dockerfile" {
		t.Errorf("dockerfile = %v, want Dockerfile", build["dockerfile"])
	}
	if node["image"] != "truffels/digibyted:9.26.5" {
		t.Errorf("image = %v, want truffels/digibyted:9.26.5", node["image"])
	}
}

func TestCatalogBuildBlock(t *testing.T) {
	ok, err := catalogBuildBlock(&BuildSpec{Dockerfile: "dockerfiles/digibyted/Dockerfile"})
	if err != nil {
		t.Fatalf("valid dockerfile rejected: %v", err)
	}
	if ok.Context != "/repo/dockerfiles/digibyted" || ok.Dockerfile != "Dockerfile" {
		t.Errorf("got context=%q dockerfile=%q", ok.Context, ok.Dockerfile)
	}
	// A dockerfile at the repo root is allowed.
	if b, err := catalogBuildBlock(&BuildSpec{Dockerfile: "Dockerfile"}); err != nil {
		t.Errorf("root Dockerfile rejected: %v", err)
	} else if b.Context != "/repo" {
		t.Errorf("root context = %q, want /repo", b.Context)
	}
	// Escapes and absolute paths must be rejected so the build context can
	// never leave /repo.
	for _, bad := range []string{"", "/etc/passwd", "../../etc/Dockerfile", "a/../../b/Dockerfile", "foo/./Dockerfile"} {
		if _, err := catalogBuildBlock(&BuildSpec{Dockerfile: bad}); err == nil {
			t.Errorf("expected rejection for dockerfile %q", bad)
		}
	}
}

// An image-only entry (no Build spec) must never carry a build block.
func TestRenderComposeImageEntryHasNoBuild(t *testing.T) {
	e := CatalogEntry{
		ID:    "imageonly",
		Image: "example/img:1.0",
		Containers: []ContainerSpec{{
			Name: "svc", User: "1000:1000", MemoryLimitMB: 128,
		}},
	}
	out, err := RenderCompose(e, map[string]any{})
	if err != nil {
		t.Fatalf("RenderCompose: %v", err)
	}
	if strings.Contains(string(out), "\"build\"") {
		t.Errorf("image-only entry emitted a build block: %s", out)
	}
}
