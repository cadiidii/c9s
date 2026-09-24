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

func sessionNamesPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "c9s", "session-names.yaml"), nil
}

// LoadSessionNames reads the sessionID -> custom label overlay used by the
// rename feature. Renaming never touches the underlying .jsonl file or its
// session ID, since Claude Code's own --resume lookup and sessionId fields
// are tied to that original UUID; the label lives entirely in this sidecar
// file instead.
func LoadSessionNames() (map[string]string, error) {
	path, err := sessionNamesPath()
	if err != nil {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return map[string]string{}, fmt.Errorf("reading %s: %w", path, err)
	}
	names := map[string]string{}
	if err := yaml.Unmarshal(data, &names); err != nil {
		return map[string]string{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return names, nil
}

// SaveSessionNames persists the sessionID -> custom label overlay.
func SaveSessionNames(names map[string]string) error {
	path, err := sessionNamesPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(names)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
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
// Code under this context: the isolated CLAUDE_CONFIG_DIR (the actual env var
// Claude Code reads - not CLAUDE_BASE_DIR, which was an earlier spec typo)
// plus any "env:VAR" indirections resolved against the current process
// environment.
func (c ContextConfig) ResolvedEnv() []string {
	out := []string{fmt.Sprintf("CLAUDE_CONFIG_DIR=%s", ResolveBaseDir(c.BaseDir))}
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
