package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// checkCacheDuration is how long to cache version check results.
	checkCacheDuration = 24 * time.Hour

	// githubReleaseURL is the GitHub API endpoint for latest release.
	githubReleaseURL = "https://api.github.com/repos/bhuneshvar-k/tributary/releases/latest"

	// userAgent is sent with GitHub API requests.
	userAgent = "tributary-cli"
)

// VersionCheck represents a cached version check result.
type VersionCheck struct {
	LatestVersion string    `json:"latest_version"`
	CheckedAt     time.Time `json:"checked_at"`
}

// CheckResult contains the result of a version check.
type CheckResult struct {
	HasUpdate    bool
	Current      string
	Latest       string
	Message      string
}

// CheckForUpdate checks if a newer version is available.
// It caches results for 24 hours to avoid excessive API calls.
// Returns the update message and true if an update is available.
// On any error, returns empty string and false (non-blocking).
func CheckForUpdate(currentVersion string) (string, bool) {
	// Skip check for dev builds
	if currentVersion == "dev" || currentVersion == "" {
		return "", false
	}

	result := checkForUpdate(currentVersion)
	if result.HasUpdate {
		return result.Message, true
	}
	return "", false
}

// checkForUpdate performs the actual version check with caching.
func checkForUpdate(currentVersion string) CheckResult {
	cachePath := getVersionCachePath()

	// Check cache first
	if cached, err := readCache(cachePath); err == nil {
		if time.Since(cached.CheckedAt) < checkCacheDuration {
			return compareVersions(currentVersion, cached.LatestVersion)
		}
	}

	// Fetch latest from GitHub API
	latest, err := fetchLatestVersion()
	if err != nil {
		// On error, try to use stale cache
		if cached, cacheErr := readCache(cachePath); cacheErr == nil {
			return compareVersions(currentVersion, cached.LatestVersion)
		}
		return CheckResult{}
	}

	// Update cache
	writeCache(cachePath, VersionCheck{
		LatestVersion: latest,
		CheckedAt:     time.Now(),
	})

	return compareVersions(currentVersion, latest)
}

// getVersionCachePath returns the path to the version check cache file.
// Uses ~/.tributary/version-check.json
func getVersionCachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "tributary-version-check.json")
	}
	return filepath.Join(home, ".tributary", "version-check.json")
}

// readCache reads the version check cache from disk.
func readCache(path string) (VersionCheck, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VersionCheck{}, err
	}

	var cached VersionCheck
	if err := json.Unmarshal(data, &cached); err != nil {
		return VersionCheck{}, err
	}

	return cached, nil
}

// writeCache writes the version check cache to disk.
func writeCache(path string, check VersionCheck) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	data, err := json.MarshalIndent(check, "", "  ")
	if err != nil {
		return
	}

	// Write atomically by writing to temp file first
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return
	}
	os.Rename(tmpPath, path)
}

// fetchLatestVersion fetches the latest release version from GitHub API.
func fetchLatestVersion() (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	req, err := http.NewRequest("GET", githubReleaseURL, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	return release.TagName, nil
}

// compareVersions compares current and latest versions.
// Returns a CheckResult with update information.
func compareVersions(current, latest string) CheckResult {
	// Normalize versions (strip leading 'v')
	current = normalizeVersion(current)
	latest = normalizeVersion(latest)

	if current == latest || latest == "" {
		return CheckResult{}
	}

	// Simple comparison: check if latest is newer
	if isNewer(latest, current) {
		return CheckResult{
			HasUpdate: true,
			Current:   current,
			Latest:    latest,
			Message:   fmt.Sprintf("New version available: v%s (current: v%s). Run 'tributary update' to upgrade.", latest, current),
		}
	}

	return CheckResult{}
}

// normalizeVersion strips the leading 'v' from a version string.
func normalizeVersion(v string) string {
	return strings.TrimPrefix(v, "v")
}

// isNewer checks if version 'a' is newer than version 'b'.
// This is a simplified comparison that handles semver (major.minor.patch).
func isNewer(a, b string) bool {
	aParts := parseVersion(a)
	bParts := parseVersion(b)

	for i := 0; i < 3; i++ {
		if aParts[i] > bParts[i] {
			return true
		}
		if aParts[i] < bParts[i] {
			return false
		}
	}
	return false
}

// parseVersion parses a version string into [major, minor, patch].
func parseVersion(v string) [3]int {
	var parts [3]int
	v = strings.TrimPrefix(v, "v")

	for i := 0; i < 3; i++ {
		idx := strings.IndexByte(v, '.')
		if idx == -1 {
			fmt.Sscanf(v, "%d", &parts[i])
			break
		}
		fmt.Sscanf(v[:idx], "%d", &parts[i])
		v = v[idx+1:]
	}
	return parts
}

// GetPlatform returns the current platform for display purposes.
func GetPlatform() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}
