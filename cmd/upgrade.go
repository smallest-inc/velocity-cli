package cmd

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/spf13/cobra"
)

const (
	releaseAPI = "https://api.github.com/repos/smallest-inc/velocity-cli/releases/latest"
)

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade vctl to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.Step(Verbose, "Checking for latest release")
		stop := ui.Spinner("Checking for updates")

		resp, err := http.Get(releaseAPI)
		stop()
		if err != nil {
			return fmt.Errorf("failed to check for updates: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return fmt.Errorf("failed to check for updates (HTTP %d)", resp.StatusCode)
		}

		var release githubRelease
		if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
			return fmt.Errorf("failed to parse release info: %w", err)
		}

		latestVersion := strings.TrimPrefix(release.TagName, "v")
		if latestVersion == version {
			ui.Success(fmt.Sprintf("Already on the latest version (%s)", version))
			return nil
		}

		ui.Info(fmt.Sprintf("Current: %s → Latest: %s", version, latestVersion))

		// Find matching asset
		targetAsset := findAsset(release.Assets)
		if targetAsset == nil {
			return fmt.Errorf("no release binary for %s/%s", runtime.GOOS, runtime.GOARCH)
		}

		ui.Step(Verbose, fmt.Sprintf("Downloading %s", targetAsset.Name))
		stop = ui.Spinner(fmt.Sprintf("Downloading %s", latestVersion))

		binResp, err := http.Get(targetAsset.BrowserDownloadURL)
		stop()
		if err != nil {
			return fmt.Errorf("failed to download: %w", err)
		}
		defer binResp.Body.Close()

		if binResp.StatusCode != 200 {
			return fmt.Errorf("download failed (HTTP %d)", binResp.StatusCode)
		}

		// Extract binary from tar.gz
		newBinary, err := extractBinary(binResp.Body)
		if err != nil {
			return fmt.Errorf("failed to extract binary: %w", err)
		}

		// Replace current binary
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to find current binary: %w", err)
		}

		ui.Step(Verbose, fmt.Sprintf("Replacing %s", execPath))

		// Write to temp file, then rename (atomic on same filesystem)
		tmpPath := execPath + ".new"
		if err := os.WriteFile(tmpPath, newBinary, 0755); err != nil {
			return fmt.Errorf("failed to write new binary: %w", err)
		}

		if err := os.Rename(tmpPath, execPath); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to replace binary: %w", err)
		}

		ui.Success(fmt.Sprintf("Upgraded to %s", latestVersion))
		return nil
	},
}

func findAsset(assets []githubAsset) *githubAsset {
	target := fmt.Sprintf("vctl_%s_%s", runtime.GOOS, runtime.GOARCH)
	for i := range assets {
		if strings.Contains(assets[i].Name, target) && strings.HasSuffix(assets[i].Name, ".tar.gz") {
			return &assets[i]
		}
	}
	return nil
}

func extractBinary(r io.Reader) ([]byte, error) {
	gz, err := gzip.NewReader(r)
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

func init() {
	rootCmd.AddCommand(upgradeCmd)
}
