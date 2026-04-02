package update

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	releaseAPI    = "https://api.github.com/repos/smallest-inc/velocity-cli/releases/latest"
	checkCooldown = 1 * time.Hour
)

type state struct {
	LastCheck      time.Time `yaml:"last_check"`
	LatestVersion  string    `yaml:"latest_version"`
	CurrentVersion string    `yaml:"current_version"`
}

type GithubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []GithubAsset `json:"assets"`
}

type GithubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func statePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vctl", "update_state.yml")
}

func loadState() *state {
	s := &state{}
	data, err := os.ReadFile(statePath())
	if err != nil {
		return s
	}
	yaml.Unmarshal(data, s)
	return s
}

func saveState(s *state) {
	data, _ := yaml.Marshal(s)
	os.WriteFile(statePath(), data, 0644)
}

// Notify returns an update message if a newer version was previously discovered
// but auto-upgrade hasn't run yet (e.g., upgrade failed or was skipped).
func Notify(currentVersion string) string {
	s := loadState()
	if s.LatestVersion != "" && s.LatestVersion != currentVersion {
		return fmt.Sprintf("Update available: %s → %s (run 'vctl upgrade')", currentVersion, s.LatestVersion)
	}
	return ""
}

// AutoUpgrade checks for a new version and silently upgrades in the background.
// Called in PersistentPostRun — runs after the user's command completes.
// Returns immediately if the cooldown hasn't passed or no update is available.
func AutoUpgrade(currentVersion string, quiet bool, enabled bool) {
	// Skip for local dev builds or when disabled via settings
	if !enabled || strings.Contains(currentVersion, "dirty") || currentVersion == "dev" {
		return
	}

	s := loadState()
	if time.Since(s.LastCheck) < checkCooldown {
		// Cooldown not passed — check if we have a cached newer version to apply
		if s.LatestVersion != "" && s.LatestVersion != currentVersion {
			doUpgrade(currentVersion, s.LatestVersion, quiet)
		}
		return
	}

	// Cooldown passed — fetch latest and upgrade if needed
	release, err := FetchLatestRelease()
	if err != nil {
		return // silent failure
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	saveState(&state{
		LastCheck:      time.Now(),
		LatestVersion:  latest,
		CurrentVersion: currentVersion,
	})

	if latest != currentVersion {
		doUpgrade(currentVersion, latest, quiet)
	}
}

// CheckInBackground spawns a goroutine to check for updates. The result is
// written to disk for Notify() or AutoUpgrade() on the next invocation.
func CheckInBackground(currentVersion string) {
	s := loadState()
	if time.Since(s.LastCheck) < checkCooldown {
		return
	}

	go func() {
		release, err := FetchLatestRelease()
		if err != nil {
			return
		}
		saveState(&state{
			LastCheck:      time.Now(),
			LatestVersion:  strings.TrimPrefix(release.TagName, "v"),
			CurrentVersion: currentVersion,
		})
	}()
}

func doUpgrade(currentVersion, latestVersion string, quiet bool) {
	release, err := FetchLatestRelease()
	if err != nil {
		return
	}

	asset := FindAsset(release.Assets)
	if asset == nil {
		return
	}

	binary, err := downloadAndExtract(asset.BrowserDownloadURL)
	if err != nil {
		return
	}

	execPath, err := os.Executable()
	if err != nil {
		return
	}

	// Atomic replace: write to temp file, then rename
	tmpPath := execPath + ".new"
	if err := os.WriteFile(tmpPath, binary, 0755); err != nil {
		return
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		return
	}

	if !quiet {
		fmt.Printf("\n✓ vctl auto-upgraded: %s → %s\n", currentVersion, latestVersion)
	}

	// Update state so we don't re-upgrade
	saveState(&state{
		LastCheck:      time.Now(),
		LatestVersion:  latestVersion,
		CurrentVersion: latestVersion,
	})
}

// FetchLatestRelease queries the GitHub API for the latest release.
func FetchLatestRelease() (*GithubRelease, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(releaseAPI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var release GithubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

// FindAsset returns the release asset matching the current OS/arch.
func FindAsset(assets []GithubAsset) *GithubAsset {
	osArch := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	for i := range assets {
		if strings.Contains(assets[i].Name, osArch) && strings.HasSuffix(assets[i].Name, ".tar.gz") {
			return &assets[i]
		}
	}
	return nil
}

func downloadAndExtract(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == "vctl" || strings.HasSuffix(hdr.Name, "/vctl") {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("vctl binary not found in archive")
}
