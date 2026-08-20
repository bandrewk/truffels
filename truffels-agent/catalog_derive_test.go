package main

import (
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	composeRoot = "/srv/truffels/compose"
	dataRoot = "/srv/truffels/data"
	configRoot = "/srv/truffels/config"
	os.Exit(m.Run())
}

func TestDerivedNames(t *testing.T) {
	if got := catContainerName("digibyted", "node"); got != "truffels-digibyted-node" {
		t.Errorf("catContainerName = %q", got)
	}
	if got := catComposeDir("digibyted"); got != "/srv/truffels/compose/cat-digibyted" {
		t.Errorf("catComposeDir = %q", got)
	}
	if got := catDataDir("digibyted"); got != "/srv/truffels/data/cat-digibyted" {
		t.Errorf("catDataDir = %q", got)
	}
	if got := catConfigPath("digibyted", "digibyte.conf"); got != "/srv/truffels/config/cat-digibyted/digibyte.conf" {
		t.Errorf("catConfigPath = %q", got)
	}
	if got := catConfigDir("digibyted"); got != "/srv/truffels/config/cat-digibyted" {
		t.Errorf("catConfigDir = %q", got)
	}
}

// The firewall: the catalog path must not be able to address a legacy
// directory. This holds constructively, because "cat-" is prepended — this
// test pins that down so that nobody accidentally removes it.
func TestCatalogPathsCannotReachLegacyDirs(t *testing.T) {
	for _, legacy := range []string{"bitcoin", "electrs", "ckpool", "mempool", "ckstats", "proxy", "truffels"} {
		if got := catComposeDir(legacy); got == "/srv/truffels/compose/"+legacy {
			t.Errorf("catComposeDir(%q) hits the legacy directory: %q", legacy, got)
		}
		if !strings.HasPrefix(catComposeDir(legacy), "/srv/truffels/compose/cat-") {
			t.Errorf("catComposeDir(%q) = %q, expected cat- prefix", legacy, catComposeDir(legacy))
		}
	}
}

func TestSafeConfigKey(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"digibyte.conf", false},
		{"../x", true},
		{"a/b", true},
		{"", true},
		{".", true},
		{"..", true},
		{"bad\x00name", true},
	}
	for _, tt := range tests {
		err := safeConfigKey(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("safeConfigKey(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

