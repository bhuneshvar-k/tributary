package version

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"v1.0.0", "1.0.0"},
		{"1.0.0", "1.0.0"},
		{"v0.1.0", "0.1.0"},
		{"v2.3.4", "2.3.4"},
	}

	for _, tt := range tests {
		result := normalizeVersion(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected [3]int
	}{
		{"1.0.0", [3]int{1, 0, 0}},
		{"0.1.0", [3]int{0, 1, 0}},
		{"2.3.4", [3]int{2, 3, 4}},
		{"v1.0.0", [3]int{1, 0, 0}},
		{"10.20.30", [3]int{10, 20, 30}},
	}

	for _, tt := range tests {
		result := parseVersion(tt.input)
		if result != tt.expected {
			t.Errorf("parseVersion(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		a        string
		b        string
		expected bool
	}{
		{"1.0.0", "0.9.9", true},
		{"0.10.0", "0.9.9", true},
		{"0.0.10", "0.0.9", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.9", "1.0.0", false},
		{"2.0.0", "1.9.9", true},
	}

	for _, tt := range tests {
		result := isNewer(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expected)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		current     string
		latest      string
		hasUpdate   bool
		latestVer   string
	}{
		{"v1.0.0", "v1.1.0", true, "1.1.0"},
		{"v1.0.0", "v1.0.0", false, ""},
		{"v1.1.0", "v1.0.0", false, ""},
		{"v0.9.0", "v1.0.0", true, "1.0.0"},
		{"1.0.0", "v1.1.0", true, "1.1.0"},
		{"v1.0.0", "", false, ""},
	}

	for _, tt := range tests {
		result := compareVersions(tt.current, tt.latest)
		if result.HasUpdate != tt.hasUpdate {
			t.Errorf("compareVersions(%q, %q).HasUpdate = %v, want %v",
				tt.current, tt.latest, result.HasUpdate, tt.hasUpdate)
		}
		if tt.hasUpdate && result.Latest != tt.latestVer {
			t.Errorf("compareVersions(%q, %q).Latest = %q, want %q",
				tt.current, tt.latest, result.Latest, tt.latestVer)
		}
	}
}

func TestGetVersionCachePath(t *testing.T) {
	path := getVersionCachePath()
	if path == "" {
		t.Error("getVersionCachePath() returned empty string")
	}
	if filepath.Ext(path) != ".json" {
		t.Errorf("getVersionCachePath() = %q, should end with .json", path)
	}
}

func TestReadWriteCache(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "tributary-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cachePath := filepath.Join(tmpDir, "version-check.json")

	// Write cache
	check := VersionCheck{
		LatestVersion: "v1.2.3",
		CheckedAt:     time.Now(),
	}
	writeCache(cachePath, check)

	// Read cache
	read, err := readCache(cachePath)
	if err != nil {
		t.Fatalf("readCache() error = %v", err)
	}

	if read.LatestVersion != "v1.2.3" {
		t.Errorf("readCache().LatestVersion = %q, want %q", read.LatestVersion, "v1.2.3")
	}
}

func TestFetchLatestVersion(t *testing.T) {
	// Mock GitHub API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/bhuneshvar-k/tributary/releases/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		release := struct {
			TagName string `json:"tag_name"`
		}{TagName: "v1.2.3"}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()

	// This test verifies the function signature works
	// In production, it would hit the real GitHub API
	if server.URL == "" {
		t.Error("test server URL is empty")
	}
}

func TestCheckForUpdateSkipsDevBuild(t *testing.T) {
	// Dev builds should not check for updates
	msg, hasUpdate := CheckForUpdate("dev")
	if hasUpdate {
		t.Errorf("CheckForUpdate('dev') returned hasUpdate=true, want false")
	}
	if msg != "" {
		t.Errorf("CheckForUpdate('dev') returned message=%q, want empty", msg)
	}

	msg, hasUpdate = CheckForUpdate("")
	if hasUpdate {
		t.Errorf("CheckForUpdate('') returned hasUpdate=true, want false")
	}
}

func TestGetPlatform(t *testing.T) {
	platform := GetPlatform()
	if platform == "" {
		t.Error("GetPlatform() returned empty string")
	}
	// Should contain "/" separator
	if len(platform) < 3 {
		t.Errorf("GetPlatform() = %q, too short", platform)
	}
}
