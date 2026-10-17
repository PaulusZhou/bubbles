package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	FeishuAppID      string `yaml:"feishu_app_id"`
	FeishuAppSecret  string `yaml:"feishu_app_secret"`
	FeishuChatID     string `yaml:"feishu_chat_id"`
	Agent            string `yaml:"agent"`               // "claude" (default) or "pi"
	ClaudePath       string `yaml:"claude_path"`         // claude CLI path (agent: claude)
	PiPath           string `yaml:"pi_path"`             // pi CLI path (agent: pi)
	PiModel          string `yaml:"pi_model"`            // optional model override passed to pi --model
	PiRPCBehavior    string `yaml:"pi_rpc_behavior"`     // mid-run message behavior: "steer" (default) or "follow_up"
	PiRPCIdleMinutes int    `yaml:"pi_rpc_idle_minutes"` // idle TTL for pi RPC sessions in minutes, default 30
	DataDir          string `yaml:"data_dir"`
	WorkDir          string `yaml:"work_dir"`
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
	if cfg.Agent == "" {
		cfg.Agent = "claude"
	}
	if cfg.ClaudePath == "" {
		cfg.ClaudePath = "claude"
	}
	if cfg.PiPath == "" {
		cfg.PiPath = "pi"
	}

	// Environment variable overrides
	if v := os.Getenv("FEISHU_APP_ID"); v != "" {
		cfg.FeishuAppID = v
	}
	if v := os.Getenv("FEISHU_APP_SECRET"); v != "" {
		cfg.FeishuAppSecret = v
	}
	if v := os.Getenv("BUBBLES_AGENT"); v != "" {
		cfg.Agent = v
	}
	if v := os.Getenv("CLAUDE_PATH"); v != "" {
		cfg.ClaudePath = v
	}
	if v := os.Getenv("PI_PATH"); v != "" {
		cfg.PiPath = v
	}
	if v := os.Getenv("BUBBLES_PI_MODEL"); v != "" {
		cfg.PiModel = v
	}
	if v := os.Getenv("BUBBLES_PI_RPC_BEHAVIOR"); v != "" {
		cfg.PiRPCBehavior = v
	}
	if v := os.Getenv("BUBBLES_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}

	// Validate required fields
	if cfg.WorkDir == "" {
		return nil, fmt.Errorf("work_dir is required in %s", path)
	}
	cfg.Agent = strings.ToLower(strings.TrimSpace(cfg.Agent))
	switch cfg.Agent {
	case "claude", "pi":
		// ok
	default:
		return nil, fmt.Errorf("agent must be \"claude\" or \"pi\", got %q", cfg.Agent)
	}
	cfg.PiRPCBehavior = strings.ToLower(strings.TrimSpace(cfg.PiRPCBehavior))
	switch cfg.PiRPCBehavior {
	case "", "steer", "follow_up":
		// ok
	default:
		return nil, fmt.Errorf("pi_rpc_behavior must be \"steer\" or \"follow_up\", got %q", cfg.PiRPCBehavior)
	}
	if cfg.PiRPCIdleMinutes < 0 {
		return nil, fmt.Errorf("pi_rpc_idle_minutes must be >= 0, got %d", cfg.PiRPCIdleMinutes)
	}

	return cfg, nil
}

// AgentBinary returns the CLI path for the configured agent.
func (c *Config) AgentBinary() string {
	if c.Agent == "pi" {
		return c.PiPath
	}
	return c.ClaudePath
}

// DBPath returns the path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "bubbles.db")
}

// PiRPCIdleTimeout returns the idle TTL for pi RPC sessions.
func (c *Config) PiRPCIdleTimeout() time.Duration {
	if c.PiRPCIdleMinutes <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(c.PiRPCIdleMinutes) * time.Minute
}
