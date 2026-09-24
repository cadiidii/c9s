package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"
)

func TestConfigFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initConfigScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features/config.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// configScenarioState holds per-scenario fixtures and results, reset in the
// Before hook so scenarios never leak state into one another.
type configScenarioState struct {
	origHome    string
	tmpHome     string
	envVarsSet  []string
	cfg         AppConfig
	loadErr     error
	resolvedEnv []string
	resolvedDir string
}

func initConfigScenario(sc *godog.ScenarioContext) {
	state := &configScenarioState{}

	sc.Before(func(ctx context.Context, s *godog.Scenario) (context.Context, error) {
		tmpHome, err := os.MkdirTemp("", "c9s-config-test-*")
		if err != nil {
			return ctx, err
		}
		*state = configScenarioState{
			origHome: os.Getenv("HOME"),
			tmpHome:  tmpHome,
		}
		if err := os.Setenv("HOME", tmpHome); err != nil {
			return ctx, err
		}
		return ctx, nil
	})

	sc.After(func(ctx context.Context, s *godog.Scenario, scenarioErr error) (context.Context, error) {
		for _, v := range state.envVarsSet {
			_ = os.Unsetenv(v)
		}
		_ = os.Setenv("HOME", state.origHome)
		_ = os.RemoveAll(state.tmpHome)
		return ctx, nil
	})

	sc.Given(`^no config file exists at "([^"]*)"$`, func(path string) error {
		// Nothing to do: the fresh temp HOME has no config dir by construction.
		return nil
	})

	sc.Given(`^a config file with:$`, func(doc *godog.DocString) error {
		cfgPath := filepath.Join(state.tmpHome, ".config", "c9s", "config.yaml")
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(cfgPath, []byte(doc.Content), 0o644)
	})

	sc.Given(`^the environment variable "([^"]*)" is set to "([^"]*)"$`, func(name, value string) error {
		state.envVarsSet = append(state.envVarsSet, name)
		return os.Setenv(name, value)
	})

	sc.When(`^I load the configuration$`, func() error {
		state.cfg, state.loadErr = LoadConfig()
		return nil
	})

	sc.When(`^I resolve the environment for the "([^"]*)" context$`, func(key string) error {
		if state.cfg.Contexts == nil {
			// This scenario resolves directly off the config file without an
			// explicit "I load the configuration" step, so load it lazily.
			state.cfg, state.loadErr = LoadConfig()
		}
		ctxCfg, ok := state.cfg.Contexts[key]
		if !ok {
			return fmt.Errorf("no context named %q in loaded config", key)
		}
		state.resolvedEnv = ctxCfg.ResolvedEnv()
		return nil
	})

	sc.When(`^I resolve the base dir "([^"]*)"$`, func(baseDir string) error {
		state.resolvedDir = ResolveBaseDir(baseDir)
		return nil
	})

	sc.Then(`^the current context is "([^"]*)"$`, func(want string) error {
		if state.cfg.CurrentContext != want {
			return fmt.Errorf("expected current context %q, got %q", want, state.cfg.CurrentContext)
		}
		return nil
	})

	sc.Then(`^the "([^"]*)" context has alias "([^"]*)"$`, func(key, want string) error {
		ctxCfg, ok := state.cfg.Contexts[key]
		if !ok {
			return fmt.Errorf("no context named %q", key)
		}
		if ctxCfg.Alias != want {
			return fmt.Errorf("expected alias %q for context %q, got %q", want, key, ctxCfg.Alias)
		}
		return nil
	})

	sc.Then(`^the "([^"]*)" context has base dir "([^"]*)"$`, func(key, want string) error {
		ctxCfg, ok := state.cfg.Contexts[key]
		if !ok {
			return fmt.Errorf("no context named %q", key)
		}
		if ctxCfg.BaseDir != want {
			return fmt.Errorf("expected base dir %q for context %q, got %q", want, key, ctxCfg.BaseDir)
		}
		return nil
	})

	sc.Then(`^loading the configuration returns an error$`, func() error {
		if state.loadErr == nil {
			return fmt.Errorf("expected LoadConfig to return an error, got nil")
		}
		return nil
	})

	sc.Then(`^the resolved environment includes "([^"]*)"$`, func(want string) error {
		for _, entry := range state.resolvedEnv {
			if entry == want {
				return nil
			}
		}
		return fmt.Errorf("expected resolved environment to include %q, got %v", want, state.resolvedEnv)
	})

	sc.Then(`^the resolved base dir equals my home directory joined with "([^"]*)"$`, func(suffix string) error {
		want := filepath.Join(state.tmpHome, suffix)
		if state.resolvedDir != want {
			return fmt.Errorf("expected resolved base dir %q, got %q", want, state.resolvedDir)
		}
		return nil
	})
}
