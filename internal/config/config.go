package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	DefaultEndpoint = "https://toggle.strata.foo"
	configFileName  = "config.yml"
	credsFileName   = "credentials"
)

// Config stores user preferences.
type Config struct {
	Endpoint  string `yaml:"endpoint"`
	ProjectID string `yaml:"project_id"`
}

// Credentials stores authentication tokens.
type Credentials struct {
	Token string `yaml:"token"`
}

// ConfigDir returns ~/.vctl/, creating it if necessary.
func ConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".vctl")
	os.MkdirAll(dir, 0700)
	return dir
}

// Load reads the config from ~/.vctl/config.yml.
// Returns a default config if the file doesn't exist.
func Load() (*Config, error) {
	cfg := &Config{
		Endpoint: DefaultEndpoint,
	}
	path := filepath.Join(ConfigDir(), configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	return cfg, nil
}

// Save writes the config to ~/.vctl/config.yml.
func Save(cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	path := filepath.Join(ConfigDir(), configFileName)
	return os.WriteFile(path, data, 0644)
}

// LoadCredentials reads the credentials from ~/.vctl/credentials.
func LoadCredentials() (*Credentials, error) {
	creds := &Credentials{}
	path := filepath.Join(ConfigDir(), credsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return creds, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// SaveCredentials writes the credentials to ~/.vctl/credentials with 0600 permissions.
func SaveCredentials(creds *Credentials) error {
	data, err := yaml.Marshal(creds)
	if err != nil {
		return err
	}
	path := filepath.Join(ConfigDir(), credsFileName)
	return os.WriteFile(path, data, 0600)
}
