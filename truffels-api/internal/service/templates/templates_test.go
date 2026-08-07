package templates

import (
	"testing"

	"truffels-api/internal/model"
)

func TestNeedsBuildTemplatesDeclareRefScheme(t *testing.T) {
	if Ckpool.UpdateSource.RefScheme != model.RefSchemeTag {
		t.Errorf("ckpool RefScheme = %q, want %q", Ckpool.UpdateSource.RefScheme, model.RefSchemeTag)
	}
	if Ckpool.UpdateSource.RepoDir != "" {
		t.Errorf("ckpool RepoDir = %q, want empty (Dockerfile clones itself)", Ckpool.UpdateSource.RepoDir)
	}
	if Ckstats.UpdateSource.RefScheme != model.RefSchemeCommit {
		t.Errorf("ckstats RefScheme = %q, want %q", Ckstats.UpdateSource.RefScheme, model.RefSchemeCommit)
	}
	if Ckstats.UpdateSource.RepoDir != "/srv/truffels/data/ckpoolstats" {
		t.Errorf("ckstats RepoDir = %q, want /srv/truffels/data/ckpoolstats", Ckstats.UpdateSource.RepoDir)
	}
}
