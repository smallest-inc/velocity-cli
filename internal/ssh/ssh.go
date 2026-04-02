package ssh

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// commonArgs returns the common SSH options used across all helpers.
func commonArgs(keyPath string) []string {
	return []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-i", keyPath,
	}
}

// Exec runs a command on the remote host and returns the combined output.
func Exec(keyPath, user, addr, command string) (string, error) {
	args := commonArgs(keyPath)
	args = append(args, fmt.Sprintf("%s@%s", user, addr), command)

	cmd := exec.Command("ssh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("ssh command failed: %w\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// ExecStream runs a command on the remote host, streaming stdout/stderr.
func ExecStream(keyPath, user, addr, command string) error {
	args := commonArgs(keyPath)
	args = append(args, fmt.Sprintf("%s@%s", user, addr), command)

	cmd := exec.Command("ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ExecInteractive replaces the current process with an interactive SSH session.
// extraArgs are split into SSH flags (starting with "-") placed before user@host,
// and a remote command placed after user@host.
func ExecInteractive(keyPath, user, addr string, extraArgs ...string) error {
	sshBin, err := findBinary("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found in PATH: %w", err)
	}

	args := []string{"ssh"}
	args = append(args, commonArgs(keyPath)...)

	// Split extraArgs: flags before user@host, command after
	var remoteCmd []string
	for _, arg := range extraArgs {
		if strings.HasPrefix(arg, "-") {
			args = append(args, arg)
		} else {
			remoteCmd = append(remoteCmd, arg)
		}
	}

	args = append(args, fmt.Sprintf("%s@%s", user, addr))
	args = append(args, remoteCmd...)

	return syscall.Exec(sshBin, args, os.Environ())
}

// CopyToRemote copies a local file to the remote host using scp.
func CopyToRemote(keyPath, user, addr, localPath, remotePath string) error {
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-i", keyPath,
		"-r",
		localPath,
		fmt.Sprintf("%s@%s:%s", user, addr, remotePath),
	}

	cmd := exec.Command("scp", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// findBinary locates a binary in PATH.
func findBinary(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		full := filepath.Join(dir, name)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", name)
}
