package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	m          model
	tmpFile    string
	tmpContent string
	sessionID  string

	resumeCtx  ContextConfig
	resumeSess SessionSummary
	resumeCmd  *exec.Cmd
	resumeErr  error

	newCmd *exec.Cmd
	newErr error

	fakeClaudeDir  string
	origPATH       string
	pathOverridden bool
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
	next, _ := m.Update(keyMsgFor(key))
	return next.(model)
}

func keyMsgFor(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

// pressKeyAndRunCmd also invokes any tea.Cmd returned by Update and feeds its
// resulting Msg back through Update once. This is safe even for
// tea.ExecProcess-wrapped commands: calling the returned Cmd closure only
// yields bubbletea's internal exec-request message - actually spawning the
// process happens later, inside Program's own run loop, which these
// headless model tests never enter. Used where a step needs to observe the
// side effect of a Cmd (e.g. a statusMsg set after a failed lookup).
func pressKeyAndRunCmd(m model, key string) model {
	next, cmd := m.Update(keyMsgFor(key))
	m2 := next.(model)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			next2, _ := m2.Update(msg)
			m2 = next2.(model)
		}
	}
	return m2
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
		w.tmpContent = ""
		w.sessionID = ""
		w.resumeCtx = ContextConfig{}
		w.resumeSess = SessionSummary{}
		w.resumeCmd = nil
		w.resumeErr = nil
		w.fakeClaudeDir = ""
		w.origPATH = ""
		w.pathOverridden = false
	})

	ctx.AfterScenario(func(sc *godog.Scenario, err error) {
		if w.tmpFile != "" {
			os.Remove(w.tmpFile)
		}
		if w.fakeClaudeDir != "" {
			os.RemoveAll(w.fakeClaudeDir)
		}
		if w.pathOverridden {
			os.Setenv("PATH", w.origPATH)
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
		w.m = pressKeyAndRunCmd(w.m, key)
		return nil
	})
	ctx.Step(`^I type "([^"]*)"$`, func(text string) error {
		for _, r := range text {
			w.m = pressKey(w.m, string(r))
		}
		return nil
	})
	ctx.Step(`^I clear the rename input$`, func() error {
		for len(w.m.renameInput) > 0 {
			w.m = pressKey(w.m, "backspace")
		}
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
		w.tmpContent = `{"type":"user","isMeta":false,"message":{"role":"user","content":"hi"}}` + "\n"
		if _, err := f.WriteString(w.tmpContent); err != nil {
			return err
		}
		w.tmpFile = f.Name()
		return nil
	})
	ctx.Step(`^the active view is the Session View with that session selected$`, func() error {
		w.sessionID = "test-session"
		sess := SessionSummary{ID: w.sessionID, Path: w.tmpFile, LastActive: time.Now()}
		w.m = model{
			config: AppConfig{
				CurrentContext: "work",
				Contexts:       map[string]ContextConfig{"work": {Alias: "claude-work", BaseDir: "~/.claude-work"}},
			},
			projects:          []ProjectSummary{{DirName: "test-project", Cwd: "/tmp/test-project", Sessions: []SessionSummary{sess}}},
			activeView:        viewSessions,
			sessionFilterProj: 0,
			cursor:            0,
			sessionNames:      map[string]string{},
		}
		return nil
	})
	ctx.Step(`^the session already has the custom label "([^"]*)"$`, func(label string) error {
		w.m.sessionNames[w.sessionID] = label
		return nil
	})
	ctx.Step(`^the active view is the Project View with a project selected$`, func() error {
		w.m = newNavTestModel("work", 1)
		w.m.sessionNames = map[string]string{}
		w.m.activeView = viewProjects
		return nil
	})
	ctx.Step(`^the active view is the Context View with a context selected$`, func() error {
		w.m = newNavTestModel("work", 1)
		w.m.sessionNames = map[string]string{}
		w.m.activeView = viewContexts
		return nil
	})
	ctx.Step(`^c9s builds the new session command for that context in the directory "([^"]*)"$`, func(dir string) error {
		w.newCmd, w.newErr = buildNewSessionCmd(w.resumeCtx, dir)
		return nil
	})
	ctx.Step(`^the new session command runs the fake "claude" executable$`, func() error {
		if w.newErr != nil {
			return fmt.Errorf("expected buildNewSessionCmd to succeed, got error: %v", w.newErr)
		}
		want := filepath.Join(w.fakeClaudeDir, "claude")
		if w.newCmd.Path != want {
			return fmt.Errorf("expected new session command to run %q, got %q", want, w.newCmd.Path)
		}
		return nil
	})
	ctx.Step(`^the new session command has no arguments$`, func() error {
		if len(w.newCmd.Args) != 1 {
			return fmt.Errorf("expected no arguments beyond the binary, got %v", w.newCmd.Args)
		}
		return nil
	})
	ctx.Step(`^the new session command starts in the directory "([^"]*)"$`, func(want string) error {
		if w.newCmd.Dir != want {
			return fmt.Errorf("expected directory %q, got %q", want, w.newCmd.Dir)
		}
		return nil
	})
	ctx.Step(`^the new session environment includes a CLAUDE_CONFIG_DIR entry for the context's base dir$`, func() error {
		want := "CLAUDE_CONFIG_DIR=" + ResolveBaseDir(w.resumeCtx.BaseDir)
		for _, e := range w.newCmd.Env {
			if e == want {
				return nil
			}
		}
		return fmt.Errorf("expected env to include %q, got %v", want, w.newCmd.Env)
	})
	ctx.Step(`^no rename prompt is open$`, func() error {
		if w.m.renaming {
			return fmt.Errorf("expected no rename prompt, but one is open")
		}
		return nil
	})
	ctx.Step(`^there is no status message$`, func() error {
		if w.m.statusMsg != "" {
			return fmt.Errorf("expected no status message, got %q", w.m.statusMsg)
		}
		return nil
	})
	ctx.Step(`^the session has the custom label "([^"]*)"$`, func(want string) error {
		got, ok := w.m.sessionNames[w.sessionID]
		if !ok {
			return fmt.Errorf("expected a custom label %q, but none is set", want)
		}
		if got != want {
			return fmt.Errorf("expected custom label %q, got %q", want, got)
		}
		return nil
	})
	ctx.Step(`^the session has no custom label$`, func() error {
		if got, ok := w.m.sessionNames[w.sessionID]; ok {
			return fmt.Errorf("expected no custom label, but found %q", got)
		}
		return nil
	})
	ctx.Step(`^the session file is unchanged on disk$`, func() error {
		data, err := os.ReadFile(w.tmpFile)
		if err != nil {
			return err
		}
		if string(data) != w.tmpContent {
			return fmt.Errorf("expected session file content unchanged, got %q", string(data))
		}
		return nil
	})

	// ---- resume: real-binary fallback and clear failure messaging ----
	ctx.Step(`^a context whose alias is not a real executable$`, func() error {
		w.resumeCtx = ContextConfig{Alias: "definitely-not-a-real-c9s-binary-xyz", BaseDir: "~/.claude-work"}
		w.resumeSess = SessionSummary{ID: "sess-fake"}
		return nil
	})
	ctx.Step(`^the current context's alias is not a real executable$`, func() error {
		w.m.config.Contexts["work"] = ContextConfig{Alias: "definitely-not-a-real-c9s-binary-xyz", BaseDir: "~/.claude-work"}
		return nil
	})
	ctx.Step(`^a fake "claude" executable on PATH$`, func() error {
		dir, err := os.MkdirTemp("", "c9s-fake-bin-*")
		if err != nil {
			return err
		}
		script := filepath.Join(dir, "claude")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			return err
		}
		w.fakeClaudeDir = dir
		w.origPATH = os.Getenv("PATH")
		w.pathOverridden = true
		return os.Setenv("PATH", dir+string(os.PathListSeparator)+w.origPATH)
	})
	ctx.Step(`^no "claude" executable exists on PATH$`, func() error {
		emptyDir, err := os.MkdirTemp("", "c9s-empty-bin-*")
		if err != nil {
			return err
		}
		w.fakeClaudeDir = emptyDir // reuse for cleanup, even though nothing lives in it
		w.origPATH = os.Getenv("PATH")
		w.pathOverridden = true
		return os.Setenv("PATH", emptyDir)
	})
	ctx.Step(`^c9s builds the resume command for that context and session$`, func() error {
		w.resumeCmd, w.resumeErr = buildResumeCmd(w.resumeCtx, w.resumeSess)
		return nil
	})
	ctx.Step(`^the resume command runs the fake "claude" executable$`, func() error {
		if w.resumeErr != nil {
			return fmt.Errorf("expected buildResumeCmd to succeed, got error: %v", w.resumeErr)
		}
		want := filepath.Join(w.fakeClaudeDir, "claude")
		if w.resumeCmd.Path != want {
			return fmt.Errorf("expected resume command to run %q, got %q", want, w.resumeCmd.Path)
		}
		return nil
	})
	ctx.Step(`^the resume environment includes a CLAUDE_CONFIG_DIR entry for the context's base dir$`, func() error {
		want := "CLAUDE_CONFIG_DIR=" + ResolveBaseDir(w.resumeCtx.BaseDir)
		for _, e := range w.resumeCmd.Env {
			if e == want {
				return nil
			}
		}
		return fmt.Errorf("expected env to include %q, got %v", want, w.resumeCmd.Env)
	})
	ctx.Step(`^the status message mentions that no executable was found$`, func() error {
		if !strings.Contains(w.m.statusMsg, "found on PATH") {
			return fmt.Errorf("expected status message to mention PATH lookup failure, got %q", w.m.statusMsg)
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
