package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	dir := DefaultDataDir()
	if dir == "" {
		t.Fatal("DefaultDataDir returned empty string")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot get home dir: %v", err)
	}

	expected := filepath.Join(home, ".bubbles")
	if dir != expected {
		t.Errorf("DefaultDataDir = %q, want %q", dir, expected)
	}
}

func TestConfig_DBPath(t *testing.T) {
	cfg := &Config{DataDir: "/tmp/test"}
	got := cfg.DBPath()
	want := filepath.Join("/tmp/test", "bubbles.db")
	if got != want {
		t.Errorf("DBPath = %q, want %q", got, want)
	}
}

func TestLoad_ReadsFromHomeDir(t *testing.T) {
	// Load() always reads from ~/.bubbles/config.yaml
	// If the file exists and has work_dir, it should succeed
	cfg, err := Load()
	if err != nil {
		// If no config file exists, that's expected
		t.Logf("Load returned error (expected if no config): %v", err)
		return
	}

	if cfg.WorkDir == "" {
		t.Error("WorkDir should not be empty if Load succeeded")
	}
	if cfg.ClaudePath == "" {
		t.Error("ClaudePath should have a default value")
	}
	if cfg.DataDir == "" {
		t.Error("DataDir should have a default value")
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Skipf("Load failed (no config file): %v", err)
	}

	// ClaudePath should default to "claude" if not set in config
	if cfg.ClaudePath == "" {
		t.Error("ClaudePath should have default value")
	}
}

func TestLoad_EnvOverrides(t *testing.T) {
	// Test that env vars are respected
	originalPath := os.Getenv("CLAUDE_PATH")
	t.Setenv("CLAUDE_PATH", "/custom/claude/path")
	defer os.Setenv("CLAUDE_PATH", originalPath)

	cfg, err := Load()
	if err != nil {
		t.Skipf("Load failed (no config file): %v", err)
	}

	if cfg.ClaudePath != "/custom/claude/path" {
		t.Errorf("ClaudePath = %q, want %q (env override)", cfg.ClaudePath, "/custom/claude/path")
	}
}

func TestLoad_FeishuEnvOverrides(t *testing.T) {
	origID := os.Getenv("FEISHU_APP_ID")
	origSecret := os.Getenv("FEISHU_APP_SECRET")
	t.Setenv("FEISHU_APP_ID", "test_id")
	t.Setenv("FEISHU_APP_SECRET", "test_secret")
	defer os.Setenv("FEISHU_APP_ID", origID)
	defer os.Setenv("FEISHU_APP_SECRET", origSecret)

	cfg, err := Load()
	if err != nil {
		t.Skipf("Load failed (no config file): %v", err)
	}

	if cfg.FeishuAppID != "test_id" {
		t.Errorf("FeishuAppID = %q, want %q", cfg.FeishuAppID, "test_id")
	}
	if cfg.FeishuAppSecret != "test_secret" {
		t.Errorf("FeishuAppSecret = %q, want %q", cfg.FeishuAppSecret, "test_secret")
	}
}

func TestLoad_DataDirEnvOverride(t *testing.T) {
	origDir := os.Getenv("BUBBLES_DATA_DIR")
	t.Setenv("BUBBLES_DATA_DIR", "/custom/data/dir")
	defer os.Setenv("BUBBLES_DATA_DIR", origDir)

	cfg, err := Load()
	if err != nil {
		t.Skipf("Load failed (no config file): %v", err)
	}

	if cfg.DataDir != "/custom/data/dir" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "/custom/data/dir")
	}
}

func TestConfig_Fields(t *testing.T) {
	cfg := &Config{
		FeishuAppID:     "app_id",
		FeishuAppSecret: "secret",
		FeishuChatID:    "chat_123",
		ClaudePath:      "/usr/bin/claude",
		DataDir:         "/data",
		WorkDir:         "/work",
	}

	if cfg.FeishuAppID != "app_id" {
		t.Errorf("FeishuAppID = %q", cfg.FeishuAppID)
	}
	if cfg.FeishuAppSecret != "secret" {
		t.Errorf("FeishuAppSecret = %q", cfg.FeishuAppSecret)
	}
	if cfg.FeishuChatID != "chat_123" {
		t.Errorf("FeishuChatID = %q", cfg.FeishuChatID)
	}
	if cfg.ClaudePath != "/usr/bin/claude" {
		t.Errorf("ClaudePath = %q", cfg.ClaudePath)
	}
	if cfg.DataDir != "/data" {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
	if cfg.WorkDir != "/work" {
		t.Errorf("WorkDir = %q", cfg.WorkDir)
	}
	if cfg.DBPath() != "/data/bubbles.db" {
		t.Errorf("DBPath = %q", cfg.DBPath())
	}
}
