// Package config loads the optional driftgcp.yaml scan configuration.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config mirrors the CLI flags for a scan, so a workspace can be defined once
// in a file instead of repeated on the command line.
type Config struct {
	State        string `yaml:"state"`
	Provider     string `yaml:"provider"`
	Project      string `yaml:"project"`
	CloudFixture string `yaml:"cloud_fixture"`
	Output       string `yaml:"output"`
	Explain      bool   `yaml:"explain"`
}

// Load reads and parses a driftgcp.yaml config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %q: %w", path, err)
	}
	return &cfg, nil
}
