package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// KeysDir returns ~/.vctl/keys/, creating it if necessary.
func KeysDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".vctl", "keys")
	os.MkdirAll(dir, 0700)
	return dir
}

// GenerateKeyPair creates an ed25519 keypair named after the instance.
// Returns the public key string (for uploading to Toggle) and the private key path.
func GenerateKeyPair(instanceName string) (publicKey string, privateKeyPath string, err error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate ed25519 key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to create SSH public key: %w", err)
	}

	publicKey = string(ssh.MarshalAuthorizedKey(sshPub))
	publicKey = publicKey[:len(publicKey)-1] // trim trailing newline
	publicKey += " vctl-" + instanceName     // add comment

	// Marshal private key to OpenSSH PEM format
	privPEM, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal private key: %w", err)
	}

	dir := KeysDir()
	baseName := "vctl-" + instanceName

	// Write private key
	privateKeyPath = filepath.Join(dir, baseName)
	if err := os.WriteFile(privateKeyPath, pem.EncodeToMemory(privPEM), 0600); err != nil {
		return "", "", fmt.Errorf("failed to write private key: %w", err)
	}

	// Write public key
	pubPath := privateKeyPath + ".pub"
	if err := os.WriteFile(pubPath, []byte(publicKey+"\n"), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write public key: %w", err)
	}

	return publicKey, privateKeyPath, nil
}

// FindKeyForInstance returns the private key path for an auto-generated instance key.
func FindKeyForInstance(instanceName string) (string, bool) {
	path := filepath.Join(KeysDir(), "vctl-"+instanceName)
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	return "", false
}

// RemoveKeyForInstance deletes the auto-generated keypair for an instance.
func RemoveKeyForInstance(instanceName string) {
	dir := KeysDir()
	baseName := "vctl-" + instanceName
	os.Remove(filepath.Join(dir, baseName))
	os.Remove(filepath.Join(dir, baseName+".pub"))
}

// ListAutoKeys returns all auto-generated key names (instance names).
func ListAutoKeys() []string {
	dir := KeysDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && len(name) > 5 && name[:5] == "vctl-" && !hasExtension(name) {
			names = append(names, name[5:]) // strip "vctl-" prefix
		}
	}
	return names
}

func hasExtension(name string) bool {
	return filepath.Ext(name) != ""
}
