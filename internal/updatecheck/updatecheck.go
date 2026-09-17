// Package updatecheck checks GitHub's releases API for a newer
// tagged version than the one currently running, so the UI can show
// a simple "update available" notice with a link. It never
// downloads or installs anything on its own — the user still runs
// the new installer manually, exactly as today.
package updatecheck

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Midnight679/kickoff-cloud-sync/internal/httpclient"
)

const releasesURL = "https://api.github.com/repos/Midnight679/kickoff-cloud-sync/releases/latest"

// Info is what the frontend needs to show an update notice.
type Info struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version,omitempty"`
	URL            string `json:"url,omitempty"`
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// Check compares currentVersion (no leading "v") against GitHub's
// latest release tag. Any failure (network, rate limit, malformed
// response) comes back as an error — treat that as "couldn't check
// right now" rather than surfacing it to the user; this is a
// convenience notice, not something worth alarming anyone over.
func Check(currentVersion string) (Info, error) {
	req, err := http.NewRequest(http.MethodGet, releasesURL, nil)
	if err != nil {
		return Info{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpclient.Client().Do(req)
	if err != nil {
		return Info{}, fmt.Errorf("contacting GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("GitHub returned %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Info{}, fmt.Errorf("decoding GitHub response: %w", err)
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	newer, err := isNewer(latest, currentVersion)
	if err != nil {
		return Info{}, err
	}

	return Info{
		Available:      newer,
		CurrentVersion: currentVersion,
		LatestVersion:  latest,
		URL:            release.HTMLURL,
	}, nil
}

// isNewer reports whether a is a newer version than b, comparing
// dot-separated numeric parts (e.g. "0.1.10" > "0.1.9") rather than
// comparing the strings directly — string comparison would get that
// specific case backwards.
func isNewer(a, b string) (bool, error) {
	aParts, err := parseVersion(a)
	if err != nil {
		return false, err
	}
	bParts, err := parseVersion(b)
	if err != nil {
		return false, err
	}
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		if aParts[i] != bParts[i] {
			return aParts[i] > bParts[i], nil
		}
	}
	return len(aParts) > len(bParts), nil
}

func parseVersion(v string) ([]int, error) {
	fields := strings.Split(v, ".")
	parts := make([]int, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("invalid version segment %q in %q", f, v)
		}
		parts[i] = n
	}
	return parts, nil
}
