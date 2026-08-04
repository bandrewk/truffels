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

// newTagListServer serves the anonymous token endpoint plus a single-page
// registry /tags/list carrying the given tag names. Paired with
// newRedirectClient, which points every outbound request at the test server.
func newTagListServer(tags ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "test/image",
			"tags": tags,
		})
	}))
}

func TestCheckLatestVersion_DockerHub_PicksFirstStableTag(t *testing.T) {
	srv := newTagListServer("latest", "v2.0.0", "v1.9.0")
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"test/image"},
	}

	got, err := CheckLatestVersion(src, "stable")
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
	srv := newTagListServer(
		"latest", "edge", "nightly",
		"v3.0.0-dev", "v2.5.0-rc1", "v2.0.0alpha1", "v1.8.0beta2",
		"v1.5.0",
	)
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"test/image"},
	}
	got, err := CheckLatestVersion(src, "stable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v1.5.0" {
		t.Errorf("expected v1.5.0, got %s", got)
	}
}

func TestCheckLatestVersion_DockerHub_NoSuitableTags(t *testing.T) {
	srv := newTagListServer("latest", "edge")
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"test/image"},
	}
	_, err := CheckLatestVersion(src, "stable")
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
	_, err := CheckLatestVersion(src, "stable")
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
	got, err := CheckLatestVersion(src, "stable")
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
	_, err := CheckLatestVersion(src, "stable")
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
	got, err := CheckLatestVersion(src, "stable")
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
	_, err := CheckLatestVersion(src, "stable")
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
	got, err := CheckLatestVersion(src, "stable")
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
	_, err := CheckLatestVersion(src, "stable")
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
	got, err := CheckLatestVersion(src, "stable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sha256:test" {
		t.Errorf("expected sha256:test, got %s", got)
	}
}

// ---------- GitHub Release ----------

func TestCheckGitHubRelease_StableChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.3.0"})
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	got, err := checkGitHubRelease("owner/repo", "stable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v0.3.0" {
		t.Errorf("expected v0.3.0, got %s", got)
	}
}

func TestCheckGitHubRelease_DevChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases" {
			t.Errorf("expected /repos/owner/repo/releases, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("per_page") != "20" {
			t.Errorf("expected per_page=20, got %s", r.URL.Query().Get("per_page"))
		}
		releases := []map[string]interface{}{
			{"tag_name": "v0.3.0-dev.1", "prerelease": true, "published_at": "2026-03-16T10:00:00Z"},
		}
		_ = json.NewEncoder(w).Encode(releases)
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	got, err := checkGitHubRelease("owner/repo", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v0.3.0-dev.1" {
		t.Errorf("expected v0.3.0-dev.1, got %s", got)
	}
}

func TestCheckGitHubRelease_DevChannel_EmptyReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	_, err := checkGitHubRelease("owner/repo", "dev")
	if err == nil {
		t.Fatal("expected error for empty releases")
	}
	if !strings.Contains(err.Error(), "no releases found") {
		t.Errorf("expected 'no releases found' in error, got: %s", err)
	}
}

func TestCheckGitHubRelease_NoReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	_, err := checkGitHubRelease("owner/repo", "stable")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "no releases found") {
		t.Errorf("expected 'no releases found' in error, got: %s", err)
	}
}

func TestCheckGitHubRelease_EmptyTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": ""})
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	_, err := checkGitHubRelease("owner/repo", "stable")
	if err == nil {
		t.Fatal("expected error for empty tag")
	}
}

func TestCheckLatestVersion_GitHubRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.2.0"})
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type: model.SourceGitHubRelease,
		Repo: "bandrewk/truffels",
	}
	got, err := CheckLatestVersion(src, "stable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v0.2.0" {
		t.Errorf("expected v0.2.0, got %s", got)
	}
}

func TestExtractCurrentVersion_GitHubRelease(t *testing.T) {
	src := &model.UpdateSource{Type: model.SourceGitHubRelease}
	got := ExtractCurrentVersion(src, "truffels/agent:v0.2.0")
	if got != "v0.2.0" {
		t.Errorf("expected v0.2.0, got %s", got)
	}
}

func TestExtractCurrentVersion_GitHubRelease_NoTag(t *testing.T) {
	src := &model.UpdateSource{Type: model.SourceGitHubRelease}
	got := ExtractCurrentVersion(src, "truffels/agent")
	if got != "unknown" {
		t.Errorf("expected unknown, got %s", got)
	}
}

// dev.17: btcpayserver/bitcoin republishes "29.2" after "31.0" was already
// published, so any listing order that favours recency puts 29.2 first. The
// check must pick 31.0 (highest version), not 29.2.
func TestCheckLatestVersion_DockerHub_PicksHighestVersionNotLastUpdated(t *testing.T) {
	// 29.2 deliberately listed before the higher versions.
	srv := newTagListServer("29.2", "31.0", "30.2", "30.1")
	defer srv.Close()
	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"btcpayserver/bitcoin"},
	}
	got, err := CheckLatestVersion(src, "stable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "31.0" {
		t.Errorf("expected 31.0 (highest version), got %s", got)
	}
}

func TestCheckLatestVersion_DockerHub_HandlesVPrefix(t *testing.T) {
	srv := newTagListServer("v3.2.1", "v3.3.1", "v3.3.0", "v3.2.0")
	defer srv.Close()
	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:   model.SourceDockerHub,
		Images: []string{"mempool/backend"},
	}
	got, _ := CheckLatestVersion(src, "stable")
	if got != "v3.3.1" {
		t.Errorf("expected v3.3.1, got %s", got)
	}
}

func TestCheckLatestVersion_DockerHub_HandlesTagFilterSuffix(t *testing.T) {
	srv := newTagListServer(
		"2.11.1-alpine", "2.11.2-alpine", "2.10.0-alpine",
		"3.0.0", // doesn't match filter
	)
	defer srv.Close()
	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	src := &model.UpdateSource{
		Type:      model.SourceDockerHub,
		Images:    []string{"caddy"},
		TagFilter: "2-alpine",
	}
	got, _ := CheckLatestVersion(src, "stable")
	if got != "2.11.2-alpine" {
		t.Errorf("expected 2.11.2-alpine, got %s", got)
	}
}

func TestListDockerHubVersions_ReturnsSortedDescending(t *testing.T) {
	srv := newTagListServer("29.2", "31.0", "30.2", "30.1", "30.2.1")
	defer srv.Close()
	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	got, err := ListDockerHubVersions("btcpayserver/bitcoin", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"31.0", "30.2.1", "30.2", "30.1", "29.2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("pos %d: got %s want %s", i, got[i], want[i])
		}
	}
}

// ---------- Docker Registry v2 tag listing ----------
//
// hub.docker.com sits behind Cloudflare bot management, which answers Go's
// HTTP client with a 403 challenge page regardless of headers. Tag discovery
// therefore goes through registry-1.docker.io, which is not bot-managed.

// registryPage is one response of a paginated /tags/list endpoint.
type registryPage struct {
	tags []string
	// next is the value served in the Link rel="next" header. Empty means
	// this is the last page.
	next string
}

// newRegistryServer serves the anonymous auth token endpoint and a paginated
// /v2/<repo>/tags/list. Pages are keyed by the "last" query parameter; the
// first page is keyed by "". Requests are recorded in *seen.
func newRegistryServer(t *testing.T, pages map[string]registryPage, seen *[]*http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Clone(r.Context()))

		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/tags/list") {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		page, ok := pages[r.URL.Query().Get("last")]
		if !ok {
			http.Error(w, "no page for last="+r.URL.Query().Get("last"), http.StatusNotFound)
			return
		}
		if page.next != "" {
			w.Header().Set("Link", "<"+r.URL.Path+"?n=1000&last="+page.next+`>; rel="next"`)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": "test/image",
			"tags": page.tags,
		})
	}))
}

func TestListDockerHubVersions_QueriesRegistryTagsList(t *testing.T) {
	var seen []*http.Request
	srv := newRegistryServer(t, map[string]registryPage{
		"": {tags: []string{"latest", "v1.9.0", "v2.0.0"}},
	}, &seen)
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	got, err := ListDockerHubVersions("test/image", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "v2.0.0" || got[1] != "v1.9.0" {
		t.Errorf("expected [v2.0.0 v1.9.0], got %v", got)
	}

	if len(seen) != 2 {
		t.Fatalf("expected 2 requests (token, tags), got %d", len(seen))
	}
	if seen[0].URL.Path != "/token" {
		t.Errorf("first request should be the token endpoint, got %s", seen[0].URL.Path)
	}
	if scope := seen[0].URL.Query().Get("scope"); scope != "repository:test/image:pull" {
		t.Errorf("unexpected token scope: %s", scope)
	}
	if seen[1].URL.Path != "/v2/test/image/tags/list" {
		t.Errorf("unexpected tags path: %s", seen[1].URL.Path)
	}
	if auth := seen[1].Header.Get("Authorization"); auth != "Bearer test-token" {
		t.Errorf("tags request missing bearer token, got %q", auth)
	}
}

func TestListDockerHubVersions_PrefixesOfficialImagesWithLibrary(t *testing.T) {
	var seen []*http.Request
	srv := newRegistryServer(t, map[string]registryPage{
		"": {tags: []string{"2.11.4-alpine"}},
	}, &seen)
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	if _, err := ListDockerHubVersions("caddy", "2-alpine"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scope := seen[0].URL.Query().Get("scope"); scope != "repository:library/caddy:pull" {
		t.Errorf("unofficial scope for official image: %s", scope)
	}
	if seen[1].URL.Path != "/v2/library/caddy/tags/list" {
		t.Errorf("unexpected tags path: %s", seen[1].URL.Path)
	}
}

func TestListDockerHubVersions_FollowsPaginationLink(t *testing.T) {
	var seen []*http.Request
	srv := newRegistryServer(t, map[string]registryPage{
		"":       {tags: []string{"v1.0.0", "v1.1.0"}, next: "v1.1.0"},
		"v1.1.0": {tags: []string{"v1.2.0", "v3.0.0"}},
	}, &seen)
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	got, err := ListDockerHubVersions("test/image", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"v3.0.0", "v1.2.0", "v1.1.0", "v1.0.0"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pos %d: got %s want %s", i, got[i], want[i])
		}
	}
}

func TestListDockerHubVersions_TokenErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	_, err := ListDockerHubVersions("test/image", "")
	if err == nil {
		t.Fatal("expected error when the token endpoint fails")
	}
	if !strings.Contains(err.Error(), "docker registry auth: HTTP 403") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListDockerHubVersions_TagsListErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
			return
		}
		http.Error(w, "too many requests", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	_, err := ListDockerHubVersions("test/image", "")
	if err == nil {
		t.Fatal("expected error when tags/list fails")
	}
	if !strings.Contains(err.Error(), "docker registry tags: HTTP 429") {
		t.Errorf("unexpected error: %v", err)
	}
}

// The registry returns the complete tag list, including the per-architecture
// variants Docker publishes alongside each release. Those parse to the same
// version as the plain tag (31.0-arm64v8 -> [31 0]), so without filtering they
// tie with it and an unstable sort can hand back an arch-pinned tag as the
// update target.
func TestListDockerHubVersions_SkipsArchVariantTags(t *testing.T) {
	var seen []*http.Request
	srv := newRegistryServer(t, map[string]registryPage{
		"": {tags: []string{
			"31.0", "31.0-amd64", "31.0-arm32v7", "31.0-arm64v8",
			"30.2", "30.2-amd64", "30.2-arm64v8",
		}},
	}, &seen)
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	got, err := ListDockerHubVersions("btcpayserver/bitcoin", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"31.0", "30.2"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pos %d: got %s want %s", i, got[i], want[i])
		}
	}
}

// An arch variant explicitly asked for via tagFilter must still be listed.
func TestListDockerHubVersions_KeepsArchVariantWhenFiltered(t *testing.T) {
	var seen []*http.Request
	srv := newRegistryServer(t, map[string]registryPage{
		"": {tags: []string{"31.0", "31.0-arm64v8", "30.2-arm64v8"}},
	}, &seen)
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	got, err := ListDockerHubVersions("btcpayserver/bitcoin", "-arm64v8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"31.0-arm64v8", "30.2-arm64v8"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pos %d: got %s want %s", i, got[i], want[i])
		}
	}
}

// Tags that parse to the same version must order deterministically, with the
// plain tag ahead of any suffixed sibling — CheckLatestVersion returns [0].
func TestListDockerHubVersions_PrefersPlainTagOnVersionTie(t *testing.T) {
	var seen []*http.Request
	srv := newRegistryServer(t, map[string]registryPage{
		"": {tags: []string{"31.0-zesty", "31.0", "31.0-abc"}},
	}, &seen)
	defer srv.Close()

	original := httpClient
	httpClient = newRedirectClient(srv)
	defer func() { httpClient = original }()

	for i := 0; i < 5; i++ {
		got, err := ListDockerHubVersions("test/image", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"31.0", "31.0-abc", "31.0-zesty"}
		if len(got) != len(want) {
			t.Fatalf("run %d: expected %v, got %v", i, want, got)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d pos %d: got %s want %s", i, j, got[j], want[j])
			}
		}
	}
}
