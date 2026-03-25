package velocity

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	specFile    = "velocity.yml"
	devOverFile = "velocity.dev.yml"
)

// ProjectSpec represents a velocity.yml project specification.
type ProjectSpec struct {
	APIVersion   string             `yaml:"apiVersion"`
	Kind         string             `yaml:"kind"`
	Metadata     Metadata           `yaml:"metadata"`
	Remote       Remote             `yaml:"remote"`
	Services     map[string]Service `yaml:"services"`
	Lifecycle    Lifecycle          `yaml:"lifecycle"`
	Sync         SyncConfig         `yaml:"sync"`
	Dependencies Dependencies       `yaml:"dependencies"`
}

// Metadata describes the project.
type Metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Team        string `yaml:"team"`
}

// Remote describes where the project lives on the instance.
type Remote struct {
	Path string `yaml:"path"`
	User string `yaml:"user"`
}

// Service describes one service in the project.
type Service struct {
	Path   string  `yaml:"path"`
	Port   int     `yaml:"port"`
	Routes []Route `yaml:"routes"`
}

// Route describes a Traefik route for a service.
type Route struct {
	Path     string `yaml:"path"`
	Priority int    `yaml:"priority"`
}

// Lifecycle contains commands for the project lifecycle.
type Lifecycle struct {
	Setup  string `yaml:"setup"`
	Start  string `yaml:"start"`
	Stop   string `yaml:"stop"`
	Health string `yaml:"health"`
}

// SyncConfig controls rsync behavior.
type SyncConfig struct {
	Exclude       []string `yaml:"exclude"`
	IncludeHidden []string `yaml:"include_hidden"`
}

// Dependencies lists external dependencies.
type Dependencies struct {
	Docker []DockerDep `yaml:"docker"`
}

// DockerDep describes a Docker container dependency.
type DockerDep struct {
	Name     string            `yaml:"name"`
	Image    string            `yaml:"image"`
	Ports    []string          `yaml:"ports"`
	Env      map[string]string `yaml:"env"`
	Platform string            `yaml:"platform"`
}

// DevOverrides holds local developer overrides from velocity.dev.yml.
type DevOverrides struct {
	Instance string  `yaml:"instance"`
	Remote   *Remote `yaml:"remote"`
}

// Load reads velocity.yml from the given directory.
// Then applies overrides from velocity.dev.yml if it exists.
func Load(dir string) (*ProjectSpec, *DevOverrides, error) {
	specPath := filepath.Join(dir, specFile)
	data, err := os.ReadFile(specPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("velocity.yml not found in %s", dir)
		}
		return nil, nil, fmt.Errorf("failed to read velocity.yml: %w", err)
	}

	var spec ProjectSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, nil, fmt.Errorf("failed to parse velocity.yml: %w", err)
	}

	// Apply dev overrides if present
	var devOver *DevOverrides
	devPath := filepath.Join(dir, devOverFile)
	devData, err := os.ReadFile(devPath)
	if err == nil {
		devOver = &DevOverrides{}
		if err := yaml.Unmarshal(devData, devOver); err != nil {
			return nil, nil, fmt.Errorf("failed to parse velocity.dev.yml: %w", err)
		}

		// Apply remote overrides
		if devOver.Remote != nil {
			if devOver.Remote.Path != "" {
				spec.Remote.Path = devOver.Remote.Path
			}
			if devOver.Remote.User != "" {
				spec.Remote.User = devOver.Remote.User
			}
		}
	}

	return &spec, devOver, nil
}

// LoadFromCwd loads velocity.yml from the current working directory.
func LoadFromCwd() (*ProjectSpec, *DevOverrides, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get working directory: %w", err)
	}
	return Load(cwd)
}
