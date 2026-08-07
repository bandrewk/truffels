package updates

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"truffels-api/internal/model"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// CheckLatestVersion queries the upstream source for the latest available version.
// The channel parameter is only used for SourceGitHubRelease: "dev" includes pre-releases,
// "stable" (or anything else) uses the /releases/latest endpoint which excludes pre-releases.
func CheckLatestVersion(src *model.UpdateSource, channel string) (string, error) {
	switch src.Type {
	case model.SourceDockerHub:
		if len(src.Images) == 0 {
			return "", fmt.Errorf("no images configured")
		}
		return checkDockerHub(src.Images[0], src.TagFilter)
	case model.SourceDockerDigest:
		if len(src.Images) == 0 {
			return "", fmt.Errorf("no images configured")
		}
		tag := src.TagFilter
		if tag == "" {
			tag = "latest"
		}
		return checkDockerDigest(src.Images[0], tag)
	case model.SourceGitHub:
		if src.RefScheme == model.RefSchemeTag {
			return checkGitTag(model.SourceGitHub, src.Repo, src.TagFilter, "")
		}
		return checkGitHub(src.Repo, src.Branch)
	case model.SourceBitbucket:
		if src.RefScheme == model.RefSchemeTag {
			return checkGitTag(model.SourceBitbucket, src.Repo, src.TagFilter, "")
		}
		return checkBitbucket(src.Repo, src.Branch)
	case model.SourceGitHubRelease:
		return checkGitHubRelease(src.Repo, channel)
	default:
		return "", fmt.Errorf("unknown source type: %s", src.Type)
	}
}

// checkDockerHub returns the highest-version stable tag available for the
// image. Delegates to ListDockerHubVersions and returns the first result.
func checkDockerHub(image string, tagFilter string) (string, error) {
	versions, err := ListDockerHubVersions(image, tagFilter)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no suitable tags found for %s", image)
	}
	return versions[0], nil
}

// registryTagPageSize is the page size requested from /tags/list. The registry
// caps this server-side, so pagination still has to be followed.
const registryTagPageSize = 1000

// registryMaxPages bounds pagination so a misbehaving registry cannot spin us
// forever. 20 pages covers every image in the service registry with room to spare.
const registryMaxPages = 20

// ListDockerHubVersions returns ALL stable tags matching the optional
// tagFilter, sorted by parsed version descending (highest first). Capped
// at 20 entries so the UI selector stays compact.
//
// Tags come from registry-1.docker.io rather than hub.docker.com: the Hub web
// API sits behind Cloudflare bot management, which serves Go's HTTP client a
// "Just a moment..." challenge page with HTTP 403 no matter what headers we
// send (UA spoofing does not help — the block is fingerprint-based). The
// registry API is not bot-managed and needs only an anonymous pull token.
//
// The registry returns bare tag names with no timestamps, which costs us
// nothing: sorting has been version-based since dev.16, when "first match by
// last_updated" was dropped for picking a recently-republished backport
// (e.g. btcpayserver/bitcoin:29.2 republished after 31.0 was the active
// release) over the actual highest version.
func ListDockerHubVersions(image string, tagFilter string) ([]string, error) {
	repo := normalizeRepo(image)

	token, err := registryToken(repo)
	if err != nil {
		return nil, err
	}

	var names []string
	next := fmt.Sprintf("https://registry-1.docker.io/v2/%s/tags/list?n=%d", repo, registryTagPageSize)
	for page := 0; next != "" && page < registryMaxPages; page++ {
		var err error
		names, next, err = fetchTagPage(next, token, names)
		if err != nil {
			return nil, err
		}
	}

	type parsed struct {
		name    string
		version []int
	}
	var parsedTags []parsed
	for _, name := range names {
		if name == "latest" || name == "edge" || name == "nightly" {
			continue
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "-dev") || strings.Contains(lower, "-rc") ||
			strings.Contains(lower, "alpha") || strings.Contains(lower, "beta") {
			continue
		}
		if isArchVariant(name, tagFilter) {
			continue
		}
		if tagFilter != "" && !matchTagFilter(name, tagFilter) {
			continue
		}
		v, ok := extractVersion(name, tagFilter)
		if !ok {
			continue
		}
		parsedTags = append(parsedTags, parsed{name: name, version: v})
	}

	// Ties are broken by name so the result is deterministic: sort.Slice is
	// not stable, and per-release variants ("31.0-foo") parse to the same
	// version as the plain tag ("31.0"). Shortest-then-lexicographic puts the
	// plain tag first, which is what CheckLatestVersion hands back as latest.
	sort.Slice(parsedTags, func(i, j int) bool {
		if c := compareVersions(parsedTags[i].version, parsedTags[j].version); c != 0 {
			return c > 0
		}
		if len(parsedTags[i].name) != len(parsedTags[j].name) {
			return len(parsedTags[i].name) < len(parsedTags[j].name)
		}
		return parsedTags[i].name < parsedTags[j].name
	})

	out := make([]string, 0, len(parsedTags))
	for _, p := range parsedTags {
		out = append(out, p.name)
	}
	if len(out) > 20 {
		out = out[:20]
	}
	return out, nil
}

// fetchTagPage appends one page of /tags/list to names and returns the URL of
// the next page ("" when the registry sent no Link rel="next" header).
func fetchTagPage(pageURL, token string, names []string) ([]string, string, error) {
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("docker registry tags: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("docker registry tags: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("docker registry tags: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("docker registry tags decode: %w", err)
	}

	// req.URL is the resolved base for the (relative) Link header the
	// registry sends, e.g. `</v2/library/postgres/tags/list?n=1000&last=17.9>`.
	return append(names, result.Tags...), nextPageURL(req.URL, resp.Header.Get("Link")), nil
}

// nextPageURL extracts the rel="next" target from a Link header and resolves
// it against base. Returns "" when there is no next page.
func nextPageURL(base *url.URL, link string) string {
	for _, part := range strings.Split(link, ",") {
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end <= start {
			continue
		}
		ref, err := url.Parse(strings.TrimSpace(part[start+1 : end]))
		if err != nil {
			continue
		}
		return base.ResolveReference(ref).String()
	}
	return ""
}

// archVariantSuffixes are the per-architecture and per-OS tag variants Docker
// images publish alongside their plain release tag. They parse to the same
// version as that tag, so they have to be dropped or they compete with it for
// "latest" — and pinning a service to an arch-specific tag breaks it on any
// other host.
var archVariantSuffixes = map[string]bool{
	"amd64": true, "x86_64": true, "i386": true, "386": true,
	"arm64": true, "arm64v8": true, "aarch64": true,
	"arm32v5": true, "arm32v6": true, "arm32v7": true,
	"armv6": true, "armv7": true, "armhf": true,
	"ppc64le": true, "s390x": true, "riscv64": true, "mips64le": true,
}

// isArchVariant reports whether tag is an architecture variant that was not
// explicitly asked for. A tagFilter naming the variant (e.g. "-arm64v8") opts
// back in, so an operator can still pin one deliberately.
func isArchVariant(tag, tagFilter string) bool {
	idx := strings.LastIndex(tag, "-")
	if idx < 0 {
		return false
	}
	suffix := tag[idx+1:]
	if !archVariantSuffixes[suffix] {
		return false
	}
	return !strings.Contains(tagFilter, suffix)
}

// normalizeRepo maps an image name to its registry repository path. Official
// images ("caddy") live under "library/".
func normalizeRepo(image string) string {
	if !strings.Contains(image, "/") {
		return "library/" + image
	}
	return image
}

// registryToken fetches an anonymous pull token for repo. Public images need
// no credentials.
func registryToken(repo string) (string, error) {
	tokenURL := fmt.Sprintf("https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s:pull", repo)
	resp, err := httpClient.Get(tokenURL)
	if err != nil {
		return "", fmt.Errorf("docker registry auth: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("docker registry auth: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("docker registry auth decode: %w", err)
	}
	return result.Token, nil
}

// extractVersion pulls a []int version from a tag name. Strips a leading "v"
// and, if a tagFilter is present, strips its non-version portion from either
// end so e.g. "2.11.2-alpine" with filter "2-alpine" parses as [2,11,2].
// Returns ok=false for tags that don't contain any numeric components.
func extractVersion(name, tagFilter string) ([]int, bool) {
	v := name
	// Strip the tagFilter's suffix if it's not a pure prefix-only filter.
	// install.sh uses filters like "2-alpine" (matches "2.X.Y-alpine") and
	// "16-alpine" (matches "16.X-alpine"). The trailing "-alpine" is the
	// non-version part we want to remove.
	if tagFilter != "" {
		// Find the first non-digit non-dot char in the filter — everything
		// from there onward is the suffix to strip.
		for i := 0; i < len(tagFilter); i++ {
			c := tagFilter[i]
			if c != '.' && (c < '0' || c > '9') {
				suffix := tagFilter[i:]
				v = strings.TrimSuffix(v, suffix)
				break
			}
		}
	}
	v = strings.TrimPrefix(v, "v")
	// Discard everything after the first non-version char (handles `2.11.2-alpha1` etc.)
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '.' && (c < '0' || c > '9') {
			v = v[:i]
			break
		}
	}
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// compareVersions returns >0 if a is higher, <0 if lower, 0 if equal.
// Shorter slices are treated as having 0s in the missing positions.
func compareVersions(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

// checkDockerDigest queries the Docker Hub registry v2 API for the remote manifest digest.
// This allows detecting image updates for floating tags (e.g. mariadb:lts) by comparing
// the remote digest against the locally running image digest.
func checkDockerDigest(image, tag string) (string, error) {
	// Official images need "library/" prefix for the registry API
	repo := normalizeRepo(image)

	// Step 1: Get auth token (anonymous, no credentials needed for public images)
	token, err := registryToken(repo)
	if err != nil {
		return "", err
	}

	// Step 2: HEAD the manifest to get the remote digest
	manifestURL := fmt.Sprintf("https://registry-1.docker.io/v2/%s/manifests/%s", repo, tag)
	req, _ := http.NewRequest("HEAD", manifestURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))

	manifestResp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("docker registry manifest: %w", err)
	}
	defer manifestResp.Body.Close()

	if manifestResp.StatusCode != 200 {
		return "", fmt.Errorf("docker registry manifest: HTTP %d for %s:%s", manifestResp.StatusCode, image, tag)
	}

	digest := manifestResp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("docker registry: no digest header for %s:%s", image, tag)
	}

	return digest, nil
}

// checkGitHub returns the latest commit SHA on a branch.
func checkGitHub(repo, branch string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/commits/%s", repo, branch)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github: HTTP %d for %s", resp.StatusCode, repo)
	}

	var result struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("github decode: %w", err)
	}

	if result.SHA == "" {
		return "", fmt.Errorf("github: empty sha for %s/%s", repo, branch)
	}

	return result.SHA[:12], nil
}

// checkBitbucket returns the latest commit hash on a branch.
func checkBitbucket(repo, branch string) (string, error) {
	url := fmt.Sprintf("https://api.bitbucket.org/2.0/repositories/%s/commits/%s?pagelen=1", repo, branch)

	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("bitbucket request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("bitbucket: HTTP %d for %s", resp.StatusCode, repo)
	}

	var result struct {
		Values []struct {
			Hash string `json:"hash"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("bitbucket decode: %w", err)
	}

	if len(result.Values) == 0 {
		return "", fmt.Errorf("bitbucket: no commits for %s/%s", repo, branch)
	}

	hash := result.Values[0].Hash
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return hash, nil
}

// checkGitTag returns the highest version tag in a git repo's tag list.
// Non-version tags are discarded by extractVersion — ckpool for instance
// carries M21/MP4/S1 tags alongside its vX.Y.Z releases.
// apiBase overrides the API host; empty means the real upstream host.
func checkGitTag(srcType model.SourceType, repo, filter, apiBase string) (string, error) {
	var url string
	switch srcType {
	case model.SourceBitbucket:
		base := apiBase
		if base == "" {
			base = "https://api.bitbucket.org"
		}
		url = fmt.Sprintf("%s/2.0/repositories/%s/refs/tags?pagelen=100", base, repo)
	case model.SourceGitHub:
		base := apiBase
		if base == "" {
			base = "https://api.github.com"
		}
		url = fmt.Sprintf("%s/repos/%s/tags?per_page=100", base, repo)
	default:
		return "", fmt.Errorf("checkGitTag: unsupported source type %s", srcType)
	}

	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("git tags request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("git tags: HTTP %d for %s", resp.StatusCode, repo)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("git tags read: %w", err)
	}

	// Bitbucket wraps the list in {"values":[...]}, GitHub returns a bare array.
	type tagEntry struct {
		Name string `json:"name"`
	}
	var names []tagEntry
	if srcType == model.SourceBitbucket {
		var wrapped struct {
			Values []tagEntry `json:"values"`
		}
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return "", fmt.Errorf("git tags decode: %w", err)
		}
		names = wrapped.Values
	} else {
		if err := json.Unmarshal(body, &names); err != nil {
			return "", fmt.Errorf("git tags decode: %w", err)
		}
	}

	best := ""
	var bestVer []int
	for _, t := range names {
		if filter != "" && !strings.HasPrefix(t.Name, filter) {
			continue
		}
		ver, ok := extractVersion(t.Name, "")
		if !ok {
			continue
		}
		if best == "" || compareVersions(ver, bestVer) > 0 {
			best, bestVer = t.Name, ver
		}
	}

	if best == "" {
		return "", fmt.Errorf("git tags: no version tags found for %s", repo)
	}
	return best, nil
}

// checkGitHubRelease returns the latest release tag name from GitHub.
// When channel is "dev", it fetches the most recent release (including pre-releases).
// Otherwise it uses /releases/latest which excludes pre-releases.
func checkGitHubRelease(repo, channel string) (string, error) {
	if channel == "dev" {
		return checkGitHubReleaseDev(repo)
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github release request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 404 {
		return "", fmt.Errorf("github release: no releases found for %s", repo)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github release: HTTP %d for %s", resp.StatusCode, repo)
	}

	var result struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("github release decode: %w", err)
	}

	if result.TagName == "" {
		return "", fmt.Errorf("github release: empty tag_name for %s", repo)
	}

	return result.TagName, nil
}

// checkGitHubReleaseDev fetches the most recent release (including pre-releases)
// by published_at timestamp. We fetch multiple results because GitHub sorts by
// tag creation date (lexicographic), which mis-orders multi-digit suffixes
// (e.g. dev.10 sorts before dev.2).
func checkGitHubReleaseDev(repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=20", repo)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github release request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github release: HTTP %d for %s", resp.StatusCode, repo)
	}

	var results []struct {
		TagName     string `json:"tag_name"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return "", fmt.Errorf("github release decode: %w", err)
	}

	if len(results) == 0 {
		return "", fmt.Errorf("github release: no releases found for %s", repo)
	}

	// Find the release with the most recent published_at timestamp.
	latest := results[0]
	for _, r := range results[1:] {
		if r.PublishedAt > latest.PublishedAt {
			latest = r
		}
	}

	if latest.TagName == "" {
		return "", fmt.Errorf("github release: empty tag_name for %s", repo)
	}

	return latest.TagName, nil
}

// matchTagFilter checks if a tag matches the filter pattern.
//
// Patterns:
//   - "16-alpine"  → tag must be: 16[.x.y]-alpine[z.w]  (version prefix + required suffix)
//   - "2-alpine"   → matches "2.11.2-alpine" but NOT "2.11.2-builder-alpine"
//   - "11."        → tag must start with "11." and contain only version chars (digits/dots)
//   - "lts"        → exact match
func matchTagFilter(tag, filter string) bool {
	// Prefix-only filter: "11." matches "11.8.6" but not "11.8.6-noble"
	if strings.HasSuffix(filter, ".") {
		if !strings.HasPrefix(tag, filter) {
			return false
		}
		// Rest must be only digits and dots (pure version, no distro suffix)
		rest := tag[len(filter):]
		for _, c := range rest {
			if c != '.' && (c < '0' || c > '9') {
				return false
			}
		}
		return len(rest) > 0
	}

	// Suffix filter: "16-alpine" or "2-alpine"
	idx := strings.Index(filter, "-")
	if idx < 0 {
		return tag == filter
	}

	prefix := filter[:idx]  // e.g. "2" or "16"
	suffix := filter[idx:]  // e.g. "-alpine"

	if !strings.HasPrefix(tag, prefix) {
		return false
	}

	// After prefix, only version chars allowed before the suffix
	rest := tag[len(prefix):]
	for len(rest) > 0 && (rest[0] == '.' || (rest[0] >= '0' && rest[0] <= '9')) {
		rest = rest[1:]
	}

	return rest == suffix
}

// ExtractCurrentVersion derives the current version from the running image tag or digest.
func ExtractCurrentVersion(src *model.UpdateSource, imageName string) string {
	switch src.Type {
	case model.SourceDockerHub:
		// imageName is like "btcpayserver/bitcoin:29.0" or "btcpayserver/bitcoin:29.0@sha256:..."
		// Strip digest suffix first
		name := imageName
		if atIdx := strings.Index(name, "@"); atIdx >= 0 {
			name = name[:atIdx]
		}
		if idx := strings.LastIndex(name, ":"); idx >= 0 {
			return name[idx+1:]
		}
		return "unknown"
	case model.SourceDockerDigest:
		// Digest is extracted directly in checkService via ImageInspect
		return ""
	case model.SourceGitHub, model.SourceBitbucket:
		// For custom builds, current version is stored in update_checks
		return ""
	case model.SourceGitHubRelease:
		// Current version is the image tag (e.g. "truffels/agent:v0.2.0" → "v0.2.0")
		name := imageName
		if atIdx := strings.Index(name, "@"); atIdx >= 0 {
			name = name[:atIdx]
		}
		if idx := strings.LastIndex(name, ":"); idx >= 0 {
			return name[idx+1:]
		}
		return "unknown"
	default:
		return "unknown"
	}
}
