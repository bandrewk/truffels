package updates

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"truffels-api/internal/model"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// CheckLatestVersion queries the upstream source for the latest available version.
func CheckLatestVersion(src *model.UpdateSource) (string, error) {
	switch src.Type {
	case model.SourceDockerHub:
		if len(src.Images) == 0 {
			return "", fmt.Errorf("no images configured")
		}
		return checkDockerHub(src.Images[0])
	case model.SourceGitHub:
		return checkGitHub(src.Repo, src.Branch)
	case model.SourceBitbucket:
		return checkBitbucket(src.Repo, src.Branch)
	default:
		return "", fmt.Errorf("unknown source type: %s", src.Type)
	}
}

// checkDockerHub fetches the latest non-latest tag sorted by last_updated.
func checkDockerHub(image string) (string, error) {
	// image is "owner/repo" e.g. "btcpayserver/bitcoin"
	url := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/tags/?page_size=100&ordering=last_updated", image)

	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("dockerhub request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("dockerhub: HTTP %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Name        string `json:"name"`
			LastUpdated string `json:"last_updated"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("dockerhub decode: %w", err)
	}

	// Find the latest stable tag (skip "latest", dev builds, etc.)
	// Results are already ordered by last_updated descending
	for _, t := range result.Results {
		name := t.Name
		if name == "latest" || name == "edge" || name == "nightly" {
			continue
		}
		// Skip dev/rc/alpha/beta tags
		lower := strings.ToLower(name)
		if strings.Contains(lower, "-dev") || strings.Contains(lower, "-rc") ||
			strings.Contains(lower, "alpha") || strings.Contains(lower, "beta") {
			continue
		}
		return name, nil
	}

	return "", fmt.Errorf("no suitable tags found for %s", image)
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	case model.SourceGitHub, model.SourceBitbucket:
		// For custom builds, current version is stored in update_checks
		return ""
	default:
		return "unknown"
	}
}
