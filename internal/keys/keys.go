package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
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

// manifest maps Toggle instance IDs to local key info.
type manifest struct {
	Instances map[string]keyEntry `yaml:"instances"` // keyed by Toggle instance ID
}

type keyEntry struct {
	PrivateKeyFile string `yaml:"private_key_file"` // filename within keys dir (not full path)
	SSHKeyID       string `yaml:"ssh_key_id"`       // Toggle SSH key ID (for cleanup)
	SSHKeyName     string `yaml:"ssh_key_name"`     // Toggle SSH key name (for cleanup)
}

func manifestPath() string {
	return filepath.Join(KeysDir(), "manifest.yml")
}

func loadManifest() *manifest {
	m := &manifest{Instances: make(map[string]keyEntry)}
	data, err := os.ReadFile(manifestPath())
	if err != nil {
		return m
	}
	yaml.Unmarshal(data, m)
	if m.Instances == nil {
		m.Instances = make(map[string]keyEntry)
	}
	return m
}

func saveManifest(m *manifest) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath(), data, 0600)
}

// GenerateKeyPair creates an ed25519 keypair with a random filename.
// Returns the public key string (for uploading) and private key path.
func GenerateKeyPair() (publicKey string, privateKeyPath string, err error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate ed25519 key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to create SSH public key: %w", err)
	}

	// Random suffix for filename uniqueness
	randBytes := make([]byte, 6)
	rand.Read(randBytes)
	baseName := "vctl-" + hex.EncodeToString(randBytes)

	publicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + baseName

	privPEM, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal private key: %w", err)
	}

	dir := KeysDir()

	privateKeyPath = filepath.Join(dir, baseName)
	if err := os.WriteFile(privateKeyPath, pem.EncodeToMemory(privPEM), 0600); err != nil {
		return "", "", fmt.Errorf("failed to write private key: %w", err)
	}

	pubPath := privateKeyPath + ".pub"
	if err := os.WriteFile(pubPath, []byte(publicKey+"\n"), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write public key: %w", err)
	}

	return publicKey, privateKeyPath, nil
}

// Register records the mapping from a Toggle instance ID to its auto-managed key.
func Register(instanceID string, privateKeyPath string, sshKeyID string, sshKeyName string) {
	m := loadManifest()
	m.Instances[instanceID] = keyEntry{
		PrivateKeyFile: filepath.Base(privateKeyPath),
		SSHKeyID:       sshKeyID,
		SSHKeyName:     sshKeyName,
	}
	saveManifest(m)
}

// FindKeyForInstance returns the private key path for an auto-managed instance key.
func FindKeyForInstance(instanceID string) (string, bool) {
	m := loadManifest()
	entry, ok := m.Instances[instanceID]
	if !ok {
		return "", false
	}
	path := filepath.Join(KeysDir(), entry.PrivateKeyFile)
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// GetSSHKeyID returns the Toggle SSH key ID for cleanup.
func GetSSHKeyID(instanceID string) (string, bool) {
	m := loadManifest()
	entry, ok := m.Instances[instanceID]
	if !ok {
		return "", false
	}
	return entry.SSHKeyID, true
}

// RemoveKeyForInstance deletes the auto-managed keypair and manifest entry.
func RemoveKeyForInstance(instanceID string) {
	m := loadManifest()
	entry, ok := m.Instances[instanceID]
	if !ok {
		return
	}
	dir := KeysDir()
	os.Remove(filepath.Join(dir, entry.PrivateKeyFile))
	os.Remove(filepath.Join(dir, entry.PrivateKeyFile+".pub"))
	delete(m.Instances, instanceID)
	saveManifest(m)
}
