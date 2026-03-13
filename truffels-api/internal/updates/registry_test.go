package updates

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"truffels-api/internal/model"
)

// ---------- ExtractCurrentVersion ----------

func TestExtractCurrentVersion_DockerHub_Tag(t *testing.T) {
	src := &model.UpdateSource{Type: model.SourceDockerHub}
	got := ExtractCurrentVersion(src, "owner/repo:v1.0")
	if got != "v1.0" {
		t.Errorf("expected v1.0, got %s", got)
	}
}

func TestExtractCurrentVersion_DockerHub_StripDigest(t *testing.T) {
	src := &model.UpdateSource{Type: model.SourceDockerHub}
	got := ExtractCurrentVersion(src, "owner/repo:v1.0@sha256:abc123def456")
	if got != "v1.0" {
		t.Errorf("expected v1.0, got %s", got)
	}
}

func TestExtractCurrentVersion_DockerHub_NoTag(t *testing.T) {
	src := &model.UpdateSource{Type: model.SourceDockerHub}
	got := ExtractCurrentVersion(src, "owner/repo")
	if got != "unknown" {
		t.Errorf("expected unknown, got %s", got)
	}
}

func TestExtractCurrentVersion_GitHub(t *testing.T) {
	src := &model.UpdateSource{Type: model.SourceGitHub}
	got := ExtractCurrentVersion(src, "whatever")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractCurrentVersion_Bitbucket(t *testing.T) {
	src := &model.UpdateSource{Type: model.SourceBitbucket}
	got := ExtractCurrentVersion(src, "whatever")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractCurrentVersion_UnknownType(t *testing.T) {
	src := &model.UpdateSource{Type: "something_else"}
	got := ExtractCurrentVersion(src, "whatever")
	if got != "unknown" {
		t.Errorf("expected unknown, got %s", got)
	}
}

// ---------- CheckLatestVersion ----------

// helper: save and restore the package-level httpClient
func withMockClient(srv *httptest.Server) func() {
	original := httpClient
	httpClient = srv.Client()
	return func() {
		httpClient = original
	}
}

func TestCheckLatestVersion_DockerHub_PicksFirstStableTag(t *testing.T) {
	tags := []struct {
		Name string `json:"name"`
	}{
		{"latest"},
		{"v2.0.0"},
		{"v1.9.0"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": tags})
	}))
	defer srv.Close()
	defer withMockClient(srv)()

	// Override the URL by using the test server address as the image name.
	// The function builds: https://hub.docker.com/v2/repositories/<image>/tags/...
	// We need to intercept at the HTTP client level. The mock client routes all
	// requests to srv regardless of host, so any image name works.
	src := &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"test/image"},
	}

	// The httpClient from httptest only talks to the test server, but the URL
	// the code builds points to hub.docker.com. We need a transport that
	// redirects all requests to the test server.
	src.Images = []string{"test/image"}
	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			// Rewrite the URL to point at our test server
			req.URL.Scheme = "http"
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			return http.DefaultTransport.RoundTrip(req)
		}),
	}

	got, err := CheckLatestVersion(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v2.0.0" {
		t.Errorf("expected v2.0.0, got %s", got)
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// newRedirectClient returns an http.Client that rewrites all requests to the given test server.
func newRedirectClient(srv *httptest.Server) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			req.URL.Scheme = "http"
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
}

func TestCheckLatestVersion_DockerHub_FiltersUnstableTags(t *testing.T) {
	tags := []struct {
		Name string `json:"name"`
	}{
		{"latest"},
		{"edge"},
		{"nightly"},
		{"v3.0.0-dev"},
		{"v2.5.0-rc1"},
		{"v2.0.0alpha1"},
		{"v1.8.0beta2"},
		{"v1.5.0"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": tags})
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"test/image"},
	}
	got, err := CheckLatestVersion(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v1.5.0" {
		t.Errorf("expected v1.5.0, got %s", got)
	}
}

func TestCheckLatestVersion_DockerHub_NoSuitableTags(t *testing.T) {
	tags := []struct {
		Name string `json:"name"`
	}{
		{"latest"},
		{"edge"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"results": tags})
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"test/image"},
	}
	_, err := CheckLatestVersion(src)
	if err == nil {
		t.Fatal("expected error for no suitable tags")
	}
	if !strings.Contains(err.Error(), "no suitable tags") {
		t.Errorf("expected 'no suitable tags' error, got: %v", err)
	}
}

func TestCheckLatestVersion_DockerHub_NoImages(t *testing.T) {
	src := &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{},
	}
	_, err := CheckLatestVersion(src)
	if err == nil {
		t.Fatal("expected error for no images")
	}
	if !strings.Contains(err.Error(), "no images configured") {
		t.Errorf("expected 'no images configured' error, got: %v", err)
	}
}

func TestCheckLatestVersion_GitHub_CommitSHA(t *testing.T) {
	fullSHA := "abcdef1234567890abcdef1234567890abcdef12"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": fullSHA})
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceGitHub,
		Repo:   "owner/repo",
		Branch: "main",
	}
	got, err := CheckLatestVersion(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != fullSHA[:12] {
		t.Errorf("expected %s, got %s", fullSHA[:12], got)
	}
}

func TestCheckLatestVersion_GitHub_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceGitHub,
		Repo:   "owner/repo",
		Branch: "main",
	}
	_, err := CheckLatestVersion(src)
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("expected HTTP 404 error, got: %v", err)
	}
}

func TestCheckLatestVersion_Bitbucket_CommitHash(t *testing.T) {
	fullHash := "1234567890abcdef1234567890abcdef12345678"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"values": []map[string]string{
				{"hash": fullHash},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceBitbucket,
		Repo:   "owner/repo",
		Branch: "master",
	}
	got, err := CheckLatestVersion(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != fullHash[:12] {
		t.Errorf("expected %s, got %s", fullHash[:12], got)
	}
}

func TestCheckLatestVersion_UnknownSourceType(t *testing.T) {
	src := &model.UpdateSource{
		Type: "ftp",
	}
	_, err := CheckLatestVersion(src)
	if err == nil {
		t.Fatal("expected error for unknown source type")
	}
	if !strings.Contains(err.Error(), "unknown source type") {
		t.Errorf("expected 'unknown source type' error, got: %v", err)
	}
}

// ---------- DockerDigest ----------

func TestExtractCurrentVersion_DockerDigest(t *testing.T) {
	src := &model.UpdateSource{Type: model.SourceDockerDigest}
	got := ExtractCurrentVersion(src, "mariadb:lts")
	if got != "" {
		t.Errorf("expected empty (digest handled in engine), got %q", got)
	}
}

func TestCheckLatestVersion_DockerDigest_Success(t *testing.T) {
	expectedDigest := "sha256:abc123def456789012345678901234567890123456789012345678901234"
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if strings.Contains(r.URL.Path, "/token") || strings.Contains(r.URL.RawQuery, "token") {
			// Token endpoint
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
			return
		}
		// Manifest HEAD endpoint
		if r.Method == "HEAD" {
			w.Header().Set("Docker-Content-Digest", expectedDigest)
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:      model.SourceDockerDigest,
		Images:    []string{"mariadb"},
		TagFilter: "lts",
	}
	got, err := CheckLatestVersion(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != expectedDigest {
		t.Errorf("expected %s, got %s", expectedDigest, got)
	}
}

func TestCheckLatestVersion_DockerDigest_NoImages(t *testing.T) {
	src := &model.UpdateSource{
		Type:   model.SourceDockerDigest,
		Images: []string{},
	}
	_, err := CheckLatestVersion(src)
	if err == nil {
		t.Fatal("expected error for no images")
	}
}

func TestCheckLatestVersion_DockerDigest_DefaultTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/token") || strings.Contains(r.URL.RawQuery, "token") {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
			return
		}
		// Verify we're requesting "latest" tag
		if r.Method == "HEAD" && strings.Contains(r.URL.Path, "/manifests/latest") {
			w.Header().Set("Docker-Content-Digest", "sha256:test")
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceDockerDigest,
		Images: []string{"nginx"},
		// TagFilter empty — should default to "latest"
	}
	got, err := CheckLatestVersion(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sha256:test" {
		t.Errorf("expected sha256:test, got %s", got)
	}
}
