package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	releaseAPI    = "https://api.github.com/repos/smallest-inc/velocity-cli/releases/latest"
	checkCooldown = 24 * time.Hour
)

type state struct {
	LastCheck      time.Time `yaml:"last_check"`
	LatestVersion  string    `yaml:"latest_version"`
	CurrentVersion string    `yaml:"current_version"`
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

// Notify returns an update message if a newer version was previously discovered.
// This is purely a local file read — zero network, zero latency.
func Notify(currentVersion string) string {
	s := loadState()
	if s.LatestVersion != "" && s.LatestVersion != currentVersion {
		return fmt.Sprintf("Update available: %s → %s (run 'vctl upgrade')", currentVersion, s.LatestVersion)
	}
	return ""
}

// CheckInBackground spawns a goroutine to check for updates if the cooldown
// has passed. The result is written to disk for the next invocation to read
// via Notify(). This never blocks the calling goroutine.
func CheckInBackground(currentVersion string) {
	s := loadState()
	if time.Since(s.LastCheck) < checkCooldown {
		return // cooldown hasn't passed, skip
	}

	go func() {
		latest, err := fetchLatestVersion()
		if err != nil {
			return // silent failure
		}
		s := &state{
			LastCheck:      time.Now(),
			LatestVersion:  latest,
			CurrentVersion: currentVersion,
		}
		saveState(s)
	}()
}

func fetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(releaseAPI)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}

	return strings.TrimPrefix(release.TagName, "v"), nil
}
