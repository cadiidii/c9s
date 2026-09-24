package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ContextConfig struct {
	Alias   string            `yaml:"alias"`
	BaseDir string            `yaml:"base_dir"`
	Env     map[string]string `yaml:"env"`
}

type AppConfig struct {
	CurrentContext string                   `yaml:"current-context"`
	Contexts       map[string]ContextConfig `yaml:"contexts"`
}

func defaultConfig() AppConfig {
	return AppConfig{
		CurrentContext: "work",
		Contexts: map[string]ContextConfig{
			"work":     {Alias: "claude-work", BaseDir: "~/.claude-work"},
			"personal": {Alias: "claude-personal", BaseDir: "~/.claude-personal"},
		},
	}
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "c9s", "config.yaml"), nil
}

// LoadConfig reads ~/.config/c9s/config.yaml, falling back to built-in
// defaults if it's missing or empty so a fresh install never panics (spec 5.4).
func LoadConfig() (AppConfig, error) {
	path, err := configPath()
	if err != nil {
		return defaultConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return defaultConfig(), fmt.Errorf("reading %s: %w", path, err)
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(cfg.Contexts) == 0 {
		return defaultConfig(), nil
	}
	return cfg, nil
}

// ResolveBaseDir expands a leading "~" to the user's home directory.
func ResolveBaseDir(baseDir string) string {
	if !strings.HasPrefix(baseDir, "~") {
		return baseDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return baseDir
	}
	return filepath.Join(home, strings.TrimPrefix(baseDir, "~"))
}

// ResolvedEnv builds the extra environment variables needed to launch Claude
// Code under this context: the isolated CLAUDE_BASE_DIR plus any "env:VAR"
// indirections resolved against the current process environment.
func (c ContextConfig) ResolvedEnv() []string {
	out := []string{fmt.Sprintf("CLAUDE_BASE_DIR=%s", ResolveBaseDir(c.BaseDir))}
	for k, v := range c.Env {
		if rest, ok := strings.CutPrefix(v, "env:"); ok {
			if val := os.Getenv(rest); val != "" {
				out = append(out, fmt.Sprintf("%s=%s", k, val))
			}
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}
