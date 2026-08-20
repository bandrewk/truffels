package service

import (
	"testing"
	"truffels-api/internal/catalog"
)

func TestCatalogEntryToTemplate(t *testing.T) {
	e := catalog.Entry{
		ID: "digibyted", DisplayName: "DigiByte Core", Description: "d",
		Role: "chain-node", Implementation: "digibyte-core", Chain: "dgb",
		ContainerNamesField: []string{"truffels-digibyted-node"},
	}
	e.Containers = append(e.Containers, struct {
		Name          string `json:"name"`
		MemoryLimitMB int    `json:"memory_limit_mb"`
	}{Name: "node", MemoryLimitMB: 2048})
	e.Resources.MemoryFloorMB = 1024

	tmpl := CatalogEntryToTemplate(e, "/srv/truffels/compose", "/srv/truffels/data")
	if tmpl.ID != "digibyted" {
		t.Errorf("ID = %q", tmpl.ID)
	}
	if len(tmpl.ContainerNames) != 1 || tmpl.ContainerNames[0] != "truffels-digibyted-node" {
		t.Errorf("ContainerNames = %v", tmpl.ContainerNames)
	}
	if tmpl.ComposeDir != "/srv/truffels/compose/cat-digibyted" {
		t.Errorf("ComposeDir = %q", tmpl.ComposeDir)
	}
	if tmpl.UpdateSource != nil {
		t.Error("UpdateSource must be nil for catalog services")
	}
}
