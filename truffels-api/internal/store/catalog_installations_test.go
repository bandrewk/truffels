package store

import "testing"

func TestCatalogInstallationRoundtrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCatalogInstallation("digibyted", "digibyted", map[string]any{"prune_gb": float64(8)}); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, ok, err := s.GetCatalogInstallation("digibyted")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.CatalogID != "digibyted" || got.Params["prune_gb"].(float64) != 8 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	list, _ := s.ListCatalogInstallations()
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}
	if err := s.RemoveCatalogInstallation("digibyted"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok, _ := s.GetCatalogInstallation("digibyted"); ok {
		t.Error("still present after remove")
	}
}
