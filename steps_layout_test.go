package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/cucumber/godog"
)

func TestLayoutFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initLayoutScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features/screen_layout.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

type layoutWorld struct {
	m      model
	width  int
	height int
	out    string
	prompt string
	narrow string
	wide   string

	terminalOutput string
}

func layoutModel(sessions int) model {
	proj := ProjectSummary{DirName: "p", Cwd: "/tmp/p"}
	for i := 0; i < sessions; i++ {
		proj.Sessions = append(proj.Sessions, SessionSummary{
			ID:         fmt.Sprintf("session-%03d-abcdef", i),
			LastPrompt: fmt.Sprintf("prompt number %d", i),
			LastActive: time.Now().Add(-time.Duration(i) * time.Minute),
		})
	}
	return model{
		config:            AppConfig{CurrentContext: "work", Contexts: map[string]ContextConfig{"work": {Alias: "claude-work", BaseDir: "~/.claude-work"}}},
		projects:          []ProjectSummary{proj},
		sessionNames:      map[string]string{},
		sessionFilterProj: 0,
		activeView:        viewSessions,
	}
}

func resize(m model, w, h int) model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(model)
}

func initLayoutScenario(ctx *godog.ScenarioContext) {
	w := &layoutWorld{}

	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		*w = layoutWorld{}
		return c, nil
	})

	ctx.Step(`^a terminal of (\d+) columns by (\d+) rows$`, func(c, r int) error {
		w.width, w.height = c, r
		return nil
	})
	ctx.Step(`^a Session View with (\d+) sessions$`, func(n int) error {
		w.m = resize(layoutModel(n), w.width, w.height)
		return nil
	})
	ctx.Step(`^the screen is rendered$`, func() error {
		w.out = w.m.View()
		return nil
	})
	ctx.Step(`^I press "([^"]*)" (\d+) times$`, func(key string, n int) error {
		for i := 0; i < n; i++ {
			w.m = pressKey(w.m, key)
		}
		w.out = w.m.View()
		return nil
	})
	ctx.Step(`^the terminal is resized to (\d+) columns by (\d+) rows$`, func(c, r int) error {
		w.m = resize(w.m, c, r)
		w.width = c
		w.out = w.m.View()
		return nil
	})
	ctx.Step(`^the output is exactly (\d+) lines tall$`, func(n int) error {
		if got := len(strings.Split(w.out, "\n")); got != n {
			return fmt.Errorf("expected %d lines, got %d", n, got)
		}
		return nil
	})
	ctx.Step(`^no line is wider than (\d+) columns$`, func(n int) error {
		for i, l := range strings.Split(w.out, "\n") {
			if lw := ansi.StringWidth(l); lw > n {
				return fmt.Errorf("line %d is %d columns wide (max %d): %q", i, lw, n, ansi.Strip(l))
			}
		}
		return nil
	})
	ctx.Step(`^the footer legend is on the last line$`, func() error {
		lines := strings.Split(ansi.Strip(w.out), "\n")
		if last := lines[len(lines)-1]; !strings.Contains(last, "<q> Quit") {
			return fmt.Errorf("last line is not the footer legend: %q", last)
		}
		return nil
	})
	ctx.Step(`^the selected row is visible on screen$`, func() error {
		want := fmt.Sprintf("session-%03d", w.m.cursor)[:8] // rows show the first 8 ID characters
		if !strings.Contains(ansi.Strip(w.out), want) {
			return fmt.Errorf("selected row %q (cursor %d) not on screen:\n%s", want, w.m.cursor, ansi.Strip(w.out))
		}
		return nil
	})

	ctx.Step(`^c9s runs and then quits$`, func() error {
		var out bytes.Buffer
		opts := append(programOptions(), tea.WithInput(nil), tea.WithOutput(&out))
		if _, err := tea.NewProgram(quitImmediately{layoutModel(1)}, opts...).Run(); err != nil {
			return err
		}
		w.terminalOutput = out.String()
		return nil
	})
	ctx.Step(`^it switches the terminal to the alternate screen$`, func() error {
		if !strings.Contains(w.terminalOutput, "\x1b[?1049h") {
			return fmt.Errorf("expected alt-screen enter sequence, got %q", w.terminalOutput)
		}
		return nil
	})
	ctx.Step(`^it switches the terminal back to the normal screen on exit$`, func() error {
		if !strings.Contains(w.terminalOutput, "\x1b[?1049l") {
			return fmt.Errorf("expected alt-screen exit sequence, got %q", w.terminalOutput)
		}
		return nil
	})

	ctx.Step(`^a session whose last prompt is 120 characters long$`, func() error {
		w.prompt = strings.Repeat("abcdefghij", 12)
		return nil
	})
	render := func(cols int) string {
		m := layoutModel(1)
		m.projects[0].Sessions[0].LastPrompt = w.prompt
		return ansi.Strip(resize(m, cols, 30).View())
	}
	ctx.Step(`^it is rendered in a terminal (\d+) columns wide$`, func(cols int) error {
		if cols < 100 {
			w.narrow = render(cols)
		} else {
			w.wide = render(cols)
		}
		return nil
	})
	ctx.Step(`^the 80 column rendering truncates the prompt$`, func() error {
		if strings.Contains(w.narrow, w.prompt) {
			return fmt.Errorf("expected truncated prompt at 80 columns")
		}
		return nil
	})
	ctx.Step(`^the 220 column rendering shows the whole prompt$`, func() error {
		if !strings.Contains(w.wide, w.prompt) {
			return fmt.Errorf("expected whole prompt at 220 columns, got:\n%s", w.wide)
		}
		return nil
	})
}

// quitImmediately wraps a model so a real tea.Program starts, renders once and
// exits, without running c9s's filesystem-scanning Init.
type quitImmediately struct{ model }

func (q quitImmediately) Init() tea.Cmd { return tea.Quit }

func (q quitImmediately) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := q.model.Update(msg)
	return quitImmediately{next.(model)}, cmd
}
