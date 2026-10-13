package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	"golang.org/x/mod/semver"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
var releaseVersionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Status struct {
	Repository     string `json:"repository"`
	RepositoryURL  string `json:"repository_url"`
	CurrentVersion string `json:"current_version"`
	Enabled        bool   `json:"enabled"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

type LatestInfo struct {
	TagName         string `json:"tag_name"`
	PublishedAt     string `json:"published_at"`
	Body            string `json:"body"`
	ReleaseURL      string `json:"release_url"`
	UpdateAvailable bool   `json:"update_available"`
	NoRelease       bool   `json:"no_release"`
}

type release struct {
	TagName     string         `json:"tag_name"`
	PublishedAt string         `json:"published_at"`
	Body        string         `json:"body"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	Assets      []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// GetStatus never contacts GitHub and always reports the actual backend update source.
func GetStatus() Status {
	repository := strings.TrimSpace(conf.AppConfig.Update.Repository)
	if repository == "" {
		repository = conf.UpdateRepository
	}
	status := Status{Repository: repository, CurrentVersion: conf.Version}
	if !repositoryPattern.MatchString(repository) {
		status.DisabledReason = "invalid_repository"
		return status
	}
	status.RepositoryURL = "https://github.com/" + repository
	switch {
	case !conf.AppConfig.Update.Enabled:
		status.DisabledReason = "disabled"
	case !releaseVersionPattern.MatchString(conf.Version):
		status.DisabledReason = "development"
	default:
		status.Enabled = true
	}
	return status
}

func fetchRelease(ctx context.Context, repository, tag string) (*release, error) {
	endpoint := "https://api.github.com/repos/" + repository + "/releases/latest"
	if tag != "" {
		endpoint = "https://api.github.com/repos/" + repository + "/releases/tags/" + url.PathEscape(tag)
	}
	body, err := doRequestWithFallback(ctx, endpoint, "application/vnd.github+json", 4<<20)
	if err != nil {
		return nil, err
	}
	var result release
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid GitHub release: %w", err)
	}
	if result.Draft || result.Prerelease || !releaseVersionPattern.MatchString(result.TagName) {
		return nil, fmt.Errorf("release must have a stable vMAJOR.MINOR.PATCH tag")
	}
	if tag != "" && result.TagName != tag {
		return nil, fmt.Errorf("release tag changed during lookup")
	}
	return &result, nil
}

func GetLatestInfo(ctx context.Context) (*LatestInfo, error) {
	status := GetStatus()
	if !status.Enabled {
		return &LatestInfo{}, nil
	}
	result, err := fetchRelease(ctx, status.Repository, "")
	var responseErr *httpError
	if errors.As(err, &responseErr) && responseErr.status == 404 {
		return &LatestInfo{NoRelease: true}, nil
	}
	if err != nil {
		return nil, err
	}
	return &LatestInfo{
		TagName: result.TagName, PublishedAt: result.PublishedAt, Body: result.Body,
		ReleaseURL:      status.RepositoryURL + "/releases/tag/" + url.PathEscape(result.TagName),
		UpdateAvailable: semver.Compare(result.TagName, conf.Version) > 0,
	}, nil
}

func downloadAsset(ctx context.Context, repository string, release *release, name string, limit int64) ([]byte, error) {
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		if asset.ID <= 0 {
			return nil, fmt.Errorf("invalid release asset %q", name)
		}
		// Asset IDs pin both the ZIP and checksum to this release, including private repositories.
		endpoint := fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/%d", repository, asset.ID)
		return doRequestWithFallback(ctx, endpoint, "application/octet-stream", limit)
	}
	return nil, fmt.Errorf("release %s is missing %s", release.TagName, name)
}

func verifyChecksum(data, checksums []byte, filename string) error {
	var expected []byte
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./") != filename {
			continue
		}
		if expected != nil {
			return fmt.Errorf("duplicate checksum for %s", filename)
		}
		var err error
		expected, err = hex.DecodeString(fields[0])
		if err != nil || len(expected) != sha256.Size {
			return fmt.Errorf("invalid SHA256 checksum for %s", filename)
		}
	}
	if expected == nil {
		return fmt.Errorf("SHA256SUMS has no entry for %s", filename)
	}
	actual := sha256.Sum256(data)
	if hex.EncodeToString(actual[:]) != hex.EncodeToString(expected) {
		return fmt.Errorf("SHA256 checksum mismatch for %s", filename)
	}
	return nil
}
