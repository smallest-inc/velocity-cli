package cmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/smallest-inc/velocity-cli/internal/ui"
	"github.com/smallest-inc/velocity-cli/internal/update"
	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade vctl to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		ui.Step(Verbose, "Checking for latest release")
		stop := ui.Spinner("Checking for updates")

		release, err := update.FetchLatestRelease()
		stop()
		if err != nil {
			return fmt.Errorf("failed to check for updates: %w", err)
		}

		latestVersion := strings.TrimPrefix(release.TagName, "v")
		if latestVersion == version {
			ui.Success(fmt.Sprintf("Already on the latest version (%s)", version))
			return nil
		}

		ui.Info(fmt.Sprintf("Current: %s → Latest: %s", version, latestVersion))

		asset := update.FindAsset(release.Assets)
		if asset == nil {
			return fmt.Errorf("no release binary for %s/%s", runtime.GOOS, runtime.GOARCH)
		}

		ui.Step(Verbose, fmt.Sprintf("Downloading %s", asset.Name))
		stop = ui.Spinner(fmt.Sprintf("Downloading %s", latestVersion))

		binResp, err := http.Get(asset.BrowserDownloadURL)
		stop()
		if err != nil {
			return fmt.Errorf("failed to download: %w", err)
		}
		defer binResp.Body.Close()

		if binResp.StatusCode != 200 {
			return fmt.Errorf("download failed (HTTP %d)", binResp.StatusCode)
		}

		newBinary, err := extractBinary(binResp.Body)
		if err != nil {
			return fmt.Errorf("failed to extract binary: %w", err)
		}

		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to find current binary: %w", err)
		}

		ui.Step(Verbose, fmt.Sprintf("Replacing %s", execPath))

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
