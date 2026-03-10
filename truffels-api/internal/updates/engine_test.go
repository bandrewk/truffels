package updates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper writes content to a temp compose file and returns its path.
func writeTempCompose(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp compose: %v", err)
	}
	return path
}

// helper reads the file back.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func TestUpdateComposeImageTags_SimpleTagUpdate(t *testing.T) {
	compose := `version: "3.8"
services:
  backend:
    image: mempool/backend:v3.2.0
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mempool/backend:v3.2.1") {
		t.Errorf("expected image tag v3.2.1, got:\n%s", got)
	}
	if strings.Contains(got, "v3.2.0") {
		t.Errorf("old tag v3.2.0 should not remain, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_DigestPinned(t *testing.T) {
	compose := `version: "3.8"
services:
  backend:
    image: mempool/backend:v3.2.0@sha256:abc123def456789012345678901234567890123456789012345678901234abcd
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mempool/backend:v3.2.1") {
		t.Errorf("expected image tag v3.2.1, got:\n%s", got)
	}
	if strings.Contains(got, "@sha256:") {
		t.Errorf("digest pin should be stripped after update, got:\n%s", got)
	}
	if strings.Contains(got, "v3.2.0") {
		t.Errorf("old tag v3.2.0 should not remain, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_MultipleImages(t *testing.T) {
	compose := `version: "3.8"
services:
  backend:
    image: mempool/backend:v3.2.0
    restart: unless-stopped
  frontend:
    image: mempool/frontend:v3.2.0
    restart: unless-stopped
  db:
    image: mariadb:lts
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	err := updateComposeImageTags(path, []string{"mempool/backend", "mempool/frontend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mempool/backend:v3.2.1") {
		t.Errorf("backend should be updated to v3.2.1, got:\n%s", got)
	}
	if !strings.Contains(got, "image: mempool/frontend:v3.2.1") {
		t.Errorf("frontend should be updated to v3.2.1, got:\n%s", got)
	}
	if !strings.Contains(got, "image: mariadb:lts") {
		t.Errorf("mariadb should remain unchanged, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_UnrelatedImageUnchanged(t *testing.T) {
	compose := `version: "3.8"
services:
  db:
    image: mariadb:lts
    restart: unless-stopped
  backend:
    image: mempool/backend:v3.2.0
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	// Only update mempool/backend, mariadb must stay
	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mariadb:lts") {
		t.Errorf("mariadb:lts should not be touched, got:\n%s", got)
	}
	if !strings.Contains(got, "image: mempool/backend:v3.2.1") {
		t.Errorf("backend should be updated, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_Rollback(t *testing.T) {
	compose := `version: "3.8"
services:
  backend:
    image: mempool/backend:v3.2.1
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)

	// Simulate rollback: revert from v3.2.1 back to v3.2.0
	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.1", "v3.2.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "image: mempool/backend:v3.2.0") {
		t.Errorf("expected rollback to v3.2.0, got:\n%s", got)
	}
	if strings.Contains(got, "v3.2.1") {
		t.Errorf("new tag v3.2.1 should not remain after rollback, got:\n%s", got)
	}
}

func TestUpdateComposeImageTags_FileNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "docker-compose.yml")

	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "read compose file") {
		t.Errorf("expected 'read compose file' in error, got: %v", err)
	}
}

func TestUpdateComposeImageTags_NoMatchingImages(t *testing.T) {
	compose := `version: "3.8"
services:
  db:
    image: mariadb:lts
    restart: unless-stopped
  proxy:
    image: caddy:2.9-alpine
    restart: unless-stopped
`
	path := writeTempCompose(t, compose)
	original := readFile(t, path)

	err := updateComposeImageTags(path, []string{"mempool/backend"}, "v3.2.0", "v3.2.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := readFile(t, path)
	if got != original {
		t.Errorf("file should be unchanged when no images match.\nbefore:\n%s\nafter:\n%s", original, got)
	}
}
