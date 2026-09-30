package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/cucumber/godog"
)

func TestSessionListFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initSessionListScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features/session_list.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

type sessionListWorld struct {
	m        model
	sessions []SessionSummary
	view     string
}

func initSessionListScenario(ctx *godog.ScenarioContext) {
	w := &sessionListWorld{}

	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		*w = sessionListWorld{m: model{
			config:            AppConfig{CurrentContext: "work", Contexts: map[string]ContextConfig{"work": {Alias: "claude-work", BaseDir: "~/.claude-work"}}},
			sessionNames:      map[string]string{},
			sessionFilterProj: 0,
			activeView:        viewSessions,
		}}
		return c, nil
	})

	add := func(id, name, prompt string) {
		w.sessions = append(w.sessions, SessionSummary{ID: id, LastPrompt: prompt, LastActive: time.Now()})
		if name != "" {
			w.m.sessionNames[id] = name
		}
		w.m.projects = []ProjectSummary{{DirName: "p", Cwd: "/tmp/p", Sessions: w.sessions}}
	}

	ctx.Step(`^a session list with a session named "([^"]*)" whose last prompt is "([^"]*)"$`, func(name, prompt string) error {
		add("named-session-id", name, prompt)
		return nil
	})
	ctx.Step(`^a session list with an unnamed session whose last prompt is "([^"]*)"$`, func(prompt string) error {
		add("plain-session-id", "", prompt)
		return nil
	})
	ctx.Step(`^a session list also containing an unnamed session whose last prompt is "([^"]*)"$`, func(prompt string) error {
		add("plain-session-id", "", prompt)
		return nil
	})
	ctx.Step(`^two projects where "([^"]*)" has a session and "([^"]*)" has a session$`, func(a, b string) error {
		mk := func(id, cwd string) ProjectSummary {
			sess := SessionSummary{ID: id, Cwd: cwd, LastPrompt: "work in " + cwd, LastActive: time.Now()}
			return ProjectSummary{DirName: cwd, Cwd: cwd, Sessions: []SessionSummary{sess}}
		}
		w.m.projects = []ProjectSummary{mk("alpha-session-id", a), mk("beta-session-id", b)}
		return nil
	})
	ctx.Step(`^I open the all-sessions list$`, func() error {
		w.m.sessionFilterProj = -1
		next, _ := w.m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
		w.m = next.(model)
		w.view = ansi.Strip(w.m.View())
		return nil
	})
	ctx.Step(`^I open the sessions of "([^"]*)"$`, func(project string) error {
		for i, p := range w.m.projects {
			if p.Cwd == project {
				w.m.sessionFilterProj = i
			}
		}
		next, _ := w.m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
		w.m = next.(model)
		w.view = ansi.Strip(w.m.View())
		return nil
	})
	ctx.Step(`^the header has a PROJECT column$`, func() error {
		if !strings.Contains(w.view, "PROJECT") {
			return fmt.Errorf("expected a PROJECT header, got:\n%s", w.view)
		}
		return nil
	})
	ctx.Step(`^the header has no PROJECT column$`, func() error {
		if strings.Contains(w.view, "PROJECT") {
			return fmt.Errorf("expected no PROJECT header, got:\n%s", w.view)
		}
		return nil
	})
	ctx.Step(`^a row shows "([^"]*)" and a row shows "([^"]*)"$`, func(a, b string) error {
		if err := rowContains(w.view, a); err != nil {
			return err
		}
		return rowContains(w.view, b)
	})
	ctx.Step(`^only the session from "([^"]*)" is listed$`, func(project string) error {
		got := w.m.visibleSessions()
		if len(got) != 1 || got[0].Cwd != project {
			return fmt.Errorf("expected only the session from %q, got %+v", project, got)
		}
		return nil
	})
	ctx.Step(`^I open the Session View$`, func() error {
		next, _ := w.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		w.m = next.(model)
		w.view = ansi.Strip(w.m.View())
		return nil
	})
	ctx.Step(`^I filter the sessions by "([^"]*)"$`, func(q string) error {
		w.m = typeFilter(w.m, q)
		return nil
	})
	ctx.Step(`^the header has a NAME column and a LAST PROMPT column$`, func() error {
		if !strings.Contains(w.view, "NAME") || !strings.Contains(w.view, "LAST PROMPT") {
			return fmt.Errorf("expected NAME and LAST PROMPT headers, got:\n%s", w.view)
		}
		return nil
	})
	ctx.Step(`^the row for that session shows "([^"]*)" and "([^"]*)"$`, func(a, b string) error {
		return rowContains(w.view, a, b)
	})
	ctx.Step(`^the row for that session shows "([^"]*)" as its name and "([^"]*)"$`, func(a, b string) error {
		return rowContains(w.view, a, b)
	})
	ctx.Step(`^only the session named "([^"]*)" is listed$`, func(name string) error {
		got := w.m.visibleSessions()
		if len(got) != 1 || w.m.sessionNames[got[0].ID] != name {
			return fmt.Errorf("expected only %q listed, got %d sessions", name, len(got))
		}
		return nil
	})
}

func rowContains(view string, want ...string) error {
	for _, line := range strings.Split(view, "\n") {
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(line, w)
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("no row contains all of %q in:\n%s", want, view)
}
