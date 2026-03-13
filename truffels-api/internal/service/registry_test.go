package service

import (
	"sort"
	"testing"
)

func TestNewRegistry_AllServices(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	all := r.All()
	if len(all) != 11 {
		t.Fatalf("expected 11 services, got %d", len(all))
	}
}

func TestRegistry_TopologicalOrder(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	all := r.All()
	expected := []string{
		"bitcoind", "electrs", "ckpool", "mempool-db", "ckstats-db",
		"mempool", "ckstats", "proxy", "truffels-agent", "truffels-api", "truffels-web",
	}
	for i, svc := range all {
		if svc.ID != expected[i] {
			t.Fatalf("position %d: expected %q, got %q", i, expected[i], svc.ID)
		}
	}
}

func TestRegistry_Get_Found(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	svc, ok := r.Get("bitcoind")
	if !ok {
		t.Fatal("expected to find bitcoind")
	}
	if svc.DisplayName != "Bitcoin Core" {
		t.Fatalf("expected Bitcoin Core, got %q", svc.DisplayName)
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestRegistry_ComposeDir(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")

	// bitcoind has explicit ComposeDir "bitcoin"
	btc, _ := r.Get("bitcoind")
	if btc.ComposeDir != "/srv/truffels/compose/bitcoin" {
		t.Fatalf("expected bitcoin compose dir, got %q", btc.ComposeDir)
	}

	// truffels-api has explicit ComposeDir "truffels"
	api, _ := r.Get("truffels-api")
	if api.ComposeDir != "/srv/truffels/compose/truffels" {
		t.Fatalf("expected truffels compose dir, got %q", api.ComposeDir)
	}

	// electrs uses ID as default dir name
	el, _ := r.Get("electrs")
	if el.ComposeDir != "/srv/truffels/compose/electrs" {
		t.Fatalf("expected electrs compose dir, got %q", el.ComposeDir)
	}
}

func TestRegistry_ValidateDependencies_NoDeps(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	err := r.ValidateDependencies("bitcoind", func(string) bool { return false })
	if err != nil {
		t.Fatalf("bitcoind has no deps, should pass: %v", err)
	}
}

func TestRegistry_ValidateDependencies_AllRunning(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	err := r.ValidateDependencies("electrs", func(id string) bool {
		return id == "bitcoind"
	})
	if err != nil {
		t.Fatalf("electrs deps running, should pass: %v", err)
	}
}

func TestRegistry_ValidateDependencies_DepNotRunning(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	err := r.ValidateDependencies("electrs", func(string) bool { return false })
	if err == nil {
		t.Fatal("expected error when dependency not running")
	}
}

func TestRegistry_ValidateDependencies_MultipleDeps(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	// mempool depends on bitcoind and electrs
	err := r.ValidateDependencies("mempool", func(id string) bool {
		return id == "bitcoind" // electrs not running
	})
	if err == nil {
		t.Fatal("expected error when electrs not running")
	}

	err = r.ValidateDependencies("mempool", func(string) bool { return true })
	if err != nil {
		t.Fatalf("all deps running, should pass: %v", err)
	}
}

func TestRegistry_ValidateDependencies_UnknownService(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	err := r.ValidateDependencies("nonexistent", func(string) bool { return true })
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
}

func TestRegistry_Dependents(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")

	// bitcoind should have electrs, ckpool, and mempool as dependents
	deps := r.Dependents("bitcoind")
	sort.Strings(deps)
	expected := []string{"ckpool", "electrs", "mempool"}
	if len(deps) != len(expected) {
		t.Fatalf("expected %d dependents, got %d: %v", len(expected), len(deps), deps)
	}
	for i, d := range deps {
		if d != expected[i] {
			t.Fatalf("expected %q at position %d, got %q", expected[i], i, d)
		}
	}
}

func TestRegistry_Dependents_NoDependents(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	deps := r.Dependents("truffels-web")
	if len(deps) != 0 {
		t.Fatalf("expected 0 dependents, got %d", len(deps))
	}
}

func TestRegistry_ReadOnlyServices(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")
	readOnly := []string{"proxy", "mempool-db", "ckstats-db"}
	for _, id := range readOnly {
		svc, ok := r.Get(id)
		if !ok {
			t.Fatalf("expected to find %s", id)
		}
		if !svc.ReadOnly {
			t.Fatalf("expected %s to be read-only", id)
		}
	}

	manageable := []string{"bitcoind", "electrs", "ckpool", "mempool", "ckstats", "truffels-agent", "truffels-api", "truffels-web"}
	for _, id := range manageable {
		svc, _ := r.Get(id)
		if svc.ReadOnly {
			t.Fatalf("expected %s to be manageable (not read-only)", id)
		}
	}
}

func TestRegistry_FloatingTagServices(t *testing.T) {
	r := NewRegistry("/srv/truffels/compose", "")

	// mempool-db uses a floating tag (mariadb:lts)
	svc, ok := r.Get("mempool-db")
	if !ok {
		t.Fatal("expected to find mempool-db")
	}
	if !svc.FloatingTag {
		t.Fatal("expected mempool-db to have FloatingTag=true")
	}

	// Other services should not be floating
	nonFloating := []string{"bitcoind", "electrs", "proxy", "ckstats-db", "truffels-api"}
	for _, id := range nonFloating {
		s, _ := r.Get(id)
		if s.FloatingTag {
			t.Fatalf("expected %s to not be floating", id)
		}
	}
}
