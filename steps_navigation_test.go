package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cucumber/godog"
)

func TestNavigationFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initNavigationScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features/navigation.feature", "features/session_actions.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// navWorld holds scenario-scoped state for the navigation/actions steps.
type navWorld struct {
	m       model
	tmpFile string
}

var viewNames = map[string]viewState{
	"Context View": viewContexts,
	"Project View": viewProjects,
	"Session View": viewSessions,
	"Query View":   viewQueries,
}

// pressKey drives the model exactly the way a real keypress would, through
// model.Update, so these tests exercise the actual navigation code path
// rather than asserting against hand-set state.
func pressKey(m model, key string) model {
	var km tea.KeyMsg
	switch key {
	case "enter":
		km = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		km = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	next, _ := m.Update(km)
	return next.(model)
}

func typeFilter(m model, query string) model {
	m = pressKey(m, "/")
	for _, r := range query {
		m = pressKey(m, string(r))
	}
	return pressKey(m, "enter")
}

func newNavTestModel(contextKey string, sessionCount int) model {
	proj := ProjectSummary{
		DirName: "test-project",
		Cwd:     "/tmp/test-project",
	}
	for i := 0; i < sessionCount; i++ {
		proj.Sessions = append(proj.Sessions, SessionSummary{
			ID:         fmt.Sprintf("session-%d", i),
			Path:       fmt.Sprintf("/tmp/session-%d.jsonl", i),
			ProjectDir: proj.DirName,
			LastActive: time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}
	return model{
		config: AppConfig{
			CurrentContext: contextKey,
			Contexts: map[string]ContextConfig{
				contextKey: {Alias: "claude-" + contextKey, BaseDir: "~/.claude-" + contextKey},
			},
		},
		projects:          []ProjectSummary{proj},
		activeView:        viewProjects,
		sessionFilterProj: -1,
		loading:           false,
	}
}

func initNavigationScenario(ctx *godog.ScenarioContext) {
	w := &navWorld{}

	ctx.BeforeScenario(func(sc *godog.Scenario) {
		w.m = model{}
		w.tmpFile = ""
	})

	ctx.AfterScenario(func(sc *godog.Scenario, err error) {
		if w.tmpFile != "" {
			os.Remove(w.tmpFile)
		}
	})

	// ---- Background ----
	ctx.Step(`^a loaded model with 1 context "([^"]*)" and 1 project containing 2 sessions$`,
		func(contextKey string) error {
			w.m = newNavTestModel(contextKey, 2)
			return nil
		})

	// ---- Given: view state setup ----
	ctx.Step(`^the active view is the Context View$`, func() error {
		w.m.activeView = viewContexts
		w.m.cursor = 0
		return nil
	})
	ctx.Step(`^the active view is the Project View$`, func() error {
		w.m.activeView = viewProjects
		w.m.cursor = 0
		return nil
	})
	ctx.Step(`^the active view is the Session View$`, func() error {
		w.m.activeView = viewSessions
		w.m.cursor = 0
		return nil
	})
	ctx.Step(`^the active view is the Query View$`, func() error {
		w.m.activeView = viewQueries
		w.m.cursor = 0
		return nil
	})

	// ---- Given: filter/project fixture setup ----
	ctx.Step(`^projects include "([^"]*)" and "([^"]*)"$`, func(a, b string) error {
		w.m.projects = []ProjectSummary{
			{DirName: a, Cwd: a, Sessions: []SessionSummary{{ID: "s-a", Path: "/tmp/s-a.jsonl", LastActive: time.Now()}}},
			{DirName: b, Cwd: b, Sessions: []SessionSummary{{ID: "s-b", Path: "/tmp/s-b.jsonl", LastActive: time.Now()}}},
		}
		return nil
	})
	ctx.Step(`^I am filtering by "([^"]*)"$`, func(q string) error {
		w.m.filterQuery = q
		w.m.filtering = false
		return nil
	})

	// ---- When: keypresses ----
	ctx.Step(`^I press "([^"]*)" on the "([^"]*)" context row$`, func(key, contextKey string) error {
		keys := w.m.contextKeys()
		for i, k := range keys {
			if k == contextKey {
				w.m.cursor = i
			}
		}
		w.m = pressKey(w.m, key)
		return nil
	})
	ctx.Step(`^I press "([^"]*)" on the first project row$`, func(key string) error {
		w.m.cursor = 0
		w.m = pressKey(w.m, key)
		return nil
	})
	ctx.Step(`^I press "([^"]*)" on the first session row$`, func(key string) error {
		w.m.cursor = 0
		w.m = pressKey(w.m, key)
		return nil
	})
	ctx.Step(`^I press "([^"]*)"$`, func(key string) error {
		w.m = pressKey(w.m, key)
		return nil
	})
	ctx.Step(`^I filter by "([^"]*)"$`, func(q string) error {
		w.m = typeFilter(w.m, q)
		return nil
	})

	// ---- Then: assertions ----
	ctx.Step(`^the active context becomes "([^"]*)"$`, func(want string) error {
		if w.m.config.CurrentContext != want {
			return fmt.Errorf("expected current context %q, got %q", want, w.m.config.CurrentContext)
		}
		return nil
	})
	ctx.Step(`^the active view becomes the (.+)$`, func(viewLabel string) error {
		want, ok := viewNames[viewLabel]
		if !ok {
			return fmt.Errorf("unknown view label %q", viewLabel)
		}
		if w.m.activeView != want {
			return fmt.Errorf("expected active view %q (%d), got %d", viewLabel, want, w.m.activeView)
		}
		return nil
	})
	ctx.Step(`^the active view remains the (.+)$`, func(viewLabel string) error {
		want, ok := viewNames[viewLabel]
		if !ok {
			return fmt.Errorf("unknown view label %q", viewLabel)
		}
		if w.m.activeView != want {
			return fmt.Errorf("expected active view to remain %q (%d), got %d", viewLabel, want, w.m.activeView)
		}
		return nil
	})
	ctx.Step(`^the sessions shown belong to that project$`, func() error {
		want := w.m.projects[0].Sessions
		got := w.m.visibleSessions()
		if len(got) != len(want) {
			return fmt.Errorf("expected %d sessions, got %d", len(want), len(got))
		}
		for i := range want {
			if got[i].ID != want[i].ID {
				return fmt.Errorf("session mismatch at %d: expected %q, got %q", i, want[i].ID, got[i].ID)
			}
		}
		return nil
	})
	ctx.Step(`^only "([^"]*)" is visible$`, func(want string) error {
		rows := w.m.visibleProjects()
		if len(rows) != 1 {
			return fmt.Errorf("expected exactly 1 visible project, got %d", len(rows))
		}
		if rows[0].Cwd != want {
			return fmt.Errorf("expected visible project %q, got %q", want, rows[0].Cwd)
		}
		return nil
	})
	ctx.Step(`^the filter is cleared$`, func() error {
		if w.m.filterQuery != "" {
			return fmt.Errorf("expected filter query to be cleared, still %q", w.m.filterQuery)
		}
		return nil
	})

	// ---- session_actions.feature ----
	ctx.Step(`^a real session file on disk$`, func() error {
		f, err := os.CreateTemp("", "c9s-session-*.jsonl")
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.WriteString(`{"type":"user","isMeta":false,"message":{"role":"user","content":"hi"}}` + "\n"); err != nil {
			return err
		}
		w.tmpFile = f.Name()
		return nil
	})
	ctx.Step(`^the active view is the Session View with that session selected$`, func() error {
		sess := SessionSummary{ID: "test-session", Path: w.tmpFile, LastActive: time.Now()}
		w.m = model{
			config: AppConfig{
				CurrentContext: "work",
				Contexts:       map[string]ContextConfig{"work": {Alias: "claude-work", BaseDir: "~/.claude-work"}},
			},
			projects:          []ProjectSummary{{DirName: "test-project", Cwd: "/tmp/test-project", Sessions: []SessionSummary{sess}}},
			activeView:        viewSessions,
			sessionFilterProj: 0,
			cursor:            0,
		}
		return nil
	})
	ctx.Step(`^the session file no longer exists on disk$`, func() error {
		if _, err := os.Stat(w.tmpFile); !os.IsNotExist(err) {
			return fmt.Errorf("expected session file to be deleted, stat err = %v", err)
		}
		return nil
	})
	ctx.Step(`^the session no longer appears in the model$`, func() error {
		for _, s := range w.m.projects[0].Sessions {
			if s.Path == w.tmpFile {
				return fmt.Errorf("expected session %q to be removed from model, still present", w.tmpFile)
			}
		}
		return nil
	})
	ctx.Step(`^the session file still exists on disk$`, func() error {
		if _, err := os.Stat(w.tmpFile); err != nil {
			return fmt.Errorf("expected session file to still exist, stat err = %v", err)
		}
		return nil
	})
	ctx.Step(`^the session still appears in the model$`, func() error {
		for _, s := range w.m.projects[0].Sessions {
			if s.Path == w.tmpFile {
				return nil
			}
		}
		return fmt.Errorf("expected session %q to still be present in model", w.tmpFile)
	})
}
