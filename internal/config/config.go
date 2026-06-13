package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultDataDir returns the default data directory for bubbles.
func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".bubbles")
}

// Config holds application configuration.
type Config struct {
	FeishuAppID     string `yaml:"feishu_app_id"`
	FeishuAppSecret string `yaml:"feishu_app_secret"`
	FeishuChatID    string `yaml:"feishu_chat_id"`
	ClaudePath      string `yaml:"claude_path"`
	DataDir         string `yaml:"data_dir"`
	WorkDir         string `yaml:"work_dir"`
}

// configPath returns the path to the config file.
func configPath() string {
	return filepath.Join(DefaultDataDir(), "config.yaml")
}

// Load reads configuration from ~/.bubbles/config.yaml.
// Environment variables override config file values if set.
func Load() (*Config, error) {
	cfg := &Config{}

	// Read config file
	path := configPath()
	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to parse %s: %v\n", path, err)
		}
	}

	// Defaults
	if cfg.DataDir == "" {
		cfg.DataDir = DefaultDataDir()
	}
	if cfg.ClaudePath == "" {
		cfg.ClaudePath = "claude"
	}

	// Environment variable overrides
	if v := os.Getenv("FEISHU_APP_ID"); v != "" {
		cfg.FeishuAppID = v
	}
	if v := os.Getenv("FEISHU_APP_SECRET"); v != "" {
		cfg.FeishuAppSecret = v
	}
	if v := os.Getenv("CLAUDE_PATH"); v != "" {
		cfg.ClaudePath = v
	}
	if v := os.Getenv("BUBBLES_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}

	// Validate required fields
	if cfg.WorkDir == "" {
		return nil, fmt.Errorf("work_dir is required in %s", path)
	}

	return cfg, nil
}

// DBPath returns the path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "bubbles.db")
}
