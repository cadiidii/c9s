package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type viewState int

const (
	viewProjects viewState = iota
	viewContexts
	viewSessions
	viewQueries
)

// projectsLoadedMsg carries the result of a background ScanProjects call so
// the filesystem walk never blocks the render loop (spec 5.2).
type projectsLoadedMsg struct {
	contextKey string
	projects   []ProjectSummary
	err        error
}

// queriesLoadedMsg carries the result of a background ParseSessionQueries
// call so re-reading a (potentially large) session file never blocks the
// render loop (spec 5.2).
type queriesLoadedMsg struct {
	sessionID string
	queries   []QueryCost
	err       error
}

type model struct {
	config   AppConfig
	projects []ProjectSummary
	loading  bool
	loadErr  error

	activeView viewState
	cursor     int

	// width/height are the terminal size from tea.WindowSizeMsg (0 until the
	// first one arrives); offset is the index of the first visible table row.
	width, height int
	offset        int

	// sessionFilterProj is the index into projects that the Session View is
	// scoped to, or -1 for the global cross-project timeline (":sess").
	sessionFilterProj int

	filtering   bool
	filterQuery string

	inCommand    bool
	commandInput string

	confirmDelete *SessionSummary
	statusMsg     string

	queries          []QueryCost
	queriesLoading   bool
	queriesErr       error
	queriesSessionID string

	// sessionNames is the sessionID -> custom label overlay written by the
	// rename feature. It never touches the underlying .jsonl file.
	sessionNames    map[string]string
	renaming        bool
	renameInput     string
	renamingSession string
}

func initialModel(cfg AppConfig, sessionNames map[string]string) model {
	if sessionNames == nil {
		sessionNames = map[string]string{}
	}
	return model{
		config:            cfg,
		activeView:        viewProjects,
		sessionFilterProj: -1,
		loading:           true,
		sessionNames:      sessionNames,
	}
}

func (m model) Init() tea.Cmd {
	return loadProjectsCmd(m.config)
}

func loadProjectsCmd(cfg AppConfig) tea.Cmd {
	return func() tea.Msg {
		ctx, ok := cfg.Contexts[cfg.CurrentContext]
		if !ok {
			return projectsLoadedMsg{contextKey: cfg.CurrentContext, err: fmt.Errorf("unknown context %q", cfg.CurrentContext)}
		}
		projects, err := ScanProjects(ResolveBaseDir(ctx.BaseDir))
		return projectsLoadedMsg{contextKey: cfg.CurrentContext, projects: projects, err: err}
	}
}

func loadQueriesCmd(sess SessionSummary) tea.Cmd {
	return func() tea.Msg {
		queries, err := ParseSessionQueries(sess.Path)
		return queriesLoadedMsg{sessionID: sess.ID, queries: queries, err: err}
	}
}

// ------------------------------------------------------------------
// UPDATE
// ------------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if nm, ok := next.(model); ok {
		nm.clampScroll()
		next = nm
	}
	return next, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case projectsLoadedMsg:
		if msg.contextKey != m.config.CurrentContext {
			return m, nil // stale response from a context we've since switched away from
		}
		m.loading = false
		m.loadErr = msg.err
		m.projects = msg.projects
		m.cursor = 0
		return m, nil

	case statusMsg:
		m.statusMsg = string(msg)
		return m, nil

	case queriesLoadedMsg:
		if msg.sessionID != m.queriesSessionID {
			return m, nil // stale response from a session we've since navigated away from
		}
		m.queriesLoading = false
		m.queriesErr = msg.err
		m.queries = msg.queries
		m.cursor = 0
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Delete confirmation modal takes over all input until answered.
	if m.confirmDelete != nil {
		switch msg.String() {
		case "y":
			target := *m.confirmDelete
			m.confirmDelete = nil
			if err := os.Remove(target.Path); err != nil {
				m.statusMsg = fmt.Sprintf("delete failed: %v", err)
			} else {
				m.statusMsg = fmt.Sprintf("deleted session %s", target.ID)
				m.removeSession(target.Path)
				if _, hadName := m.sessionNames[target.ID]; hadName {
					delete(m.sessionNames, target.ID)
					_ = SaveSessionNames(m.sessionNames)
				}
			}
		default:
			m.confirmDelete = nil
			m.statusMsg = "delete cancelled"
		}
		return m, nil
	}

	if m.renaming {
		return m.handleRenameKey(msg)
	}

	if m.inCommand {
		return m.handleCommandKey(msg)
	}

	if m.filtering {
		return m.handleFilterKey(msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit

	case ":":
		m.inCommand = true
		m.commandInput = ""
		m.statusMsg = ""
		return m, nil

	case "/":
		m.filtering = true
		m.filterQuery = ""
		return m, nil

	case "esc":
		if m.filterQuery != "" {
			m.filterQuery = ""
			m.cursor = 0
			return m, nil
		}
		switch m.activeView {
		case viewQueries:
			m.activeView = viewSessions
			m.cursor = 0
		case viewSessions:
			m.activeView = viewProjects
			m.sessionFilterProj = -1
			m.cursor = 0
		}
		return m, nil

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if max := m.rowCount() - 1; m.cursor < max {
			m.cursor++
		}
		return m, nil

	case "enter":
		return m.handleEnter()

	case "r":
		return m.handleResume()

	case "v":
		return m.handleViewLog()

	case "d":
		return m.handleDeletePrompt()

	case "n":
		return m.handleNewSession()

	case "R":
		return m.handleRenamePrompt()
	}
	return m, nil
}

func (m model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		cmd := strings.TrimSpace(m.commandInput)
		m.inCommand = false
		m.commandInput = ""
		switch cmd {
		case "ctx":
			m.activeView = viewContexts
			m.cursor = 0
		case "proj":
			m.activeView = viewProjects
			m.cursor = 0
		case "sess":
			m.activeView = viewSessions
			m.sessionFilterProj = -1
			m.cursor = 0
		case "q":
			return m, tea.Quit
		default:
			m.statusMsg = fmt.Sprintf("unknown command: :%s", cmd)
		}
		return m, nil
	case "esc":
		m.inCommand = false
		m.commandInput = ""
		return m, nil
	case "backspace":
		if len(m.commandInput) > 0 {
			m.commandInput = m.commandInput[:len(m.commandInput)-1]
		}
		return m, nil
	default:
		m.commandInput += msg.String()
		return m, nil
	}
}

func (m model) handleRenameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.renaming = false
		label := strings.TrimSpace(m.renameInput)
		if m.sessionNames == nil {
			m.sessionNames = map[string]string{}
		}
		if label == "" {
			delete(m.sessionNames, m.renamingSession)
		} else {
			m.sessionNames[m.renamingSession] = label
		}
		if err := SaveSessionNames(m.sessionNames); err != nil {
			m.statusMsg = fmt.Sprintf("rename save failed: %v", err)
		} else {
			m.statusMsg = "renamed"
		}
		m.renamingSession = ""
		m.renameInput = ""
		return m, nil
	case "esc":
		m.renaming = false
		m.renamingSession = ""
		m.renameInput = ""
		return m, nil
	case "backspace":
		if len(m.renameInput) > 0 {
			m.renameInput = m.renameInput[:len(m.renameInput)-1]
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			m.renameInput += msg.String()
		}
		return m, nil
	}
}

func (m model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "esc":
		m.filtering = false
		m.cursor = 0
		return m, nil
	case "backspace":
		if len(m.filterQuery) > 0 {
			m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			m.filterQuery += msg.String()
			m.cursor = 0
		}
		return m, nil
	}
}

func (m model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.activeView {
	case viewContexts:
		keys := m.contextKeys()
		if m.cursor >= len(keys) {
			return m, nil
		}
		chosen := keys[m.cursor]
		m.activeView = viewProjects
		m.cursor = 0
		if chosen == m.config.CurrentContext {
			// Already on this context - just return to the Project View,
			// same as any other selection, without a redundant rescan.
			return m, nil
		}
		m.config.CurrentContext = chosen
		m.loading = true
		m.projects = nil
		m.statusMsg = fmt.Sprintf("switched context to %s", chosen)
		return m, loadProjectsCmd(m.config)

	case viewProjects:
		rows := m.visibleProjects()
		if m.cursor >= len(rows) {
			return m, nil
		}
		m.sessionFilterProj = m.projectIndex(rows[m.cursor])
		m.activeView = viewSessions
		m.cursor = 0
		m.filterQuery = ""

	case viewSessions:
		sessions := m.visibleSessions()
		if m.cursor >= len(sessions) {
			return m, nil
		}
		sess := sessions[m.cursor]
		m.queriesSessionID = sess.ID
		m.queriesLoading = true
		m.queriesErr = nil
		m.queries = nil
		m.activeView = viewQueries
		m.cursor = 0
		return m, loadQueriesCmd(sess)
	}
	return m, nil
}

// buildResumeCmd resolves the real binary to exec for resuming a session and
// constructs the command. Split out from handleResume so the binary
// resolution / env / cwd logic is unit-testable without going through
// bubbletea's ExecProcess runtime plumbing (which only actually spawns the
// process inside the Program's own event loop, not in any Cmd closure a test
// could call directly).
//
// ctx.Alias is often a shell alias/function (e.g. zsh's
// `alias claude-work='CLAUDE_CONFIG_DIR=... command claude'`), not a real
// executable - exec.Command bypasses the shell entirely, so it can never
// resolve one. Try it as a literal binary first (covers setups where it
// really is one), then fall back to the real "claude" binary with
// CLAUDE_CONFIG_DIR set via ResolvedEnv, which is what the alias would have
// done anyway.
func buildResumeCmd(ctx ContextConfig, sess SessionSummary) (*exec.Cmd, error) {
	return buildClaudeCmd(ctx, sess.Cwd, "--resume", sess.ID)
}

// buildNewSessionCmd starts a fresh claude session for the context, in dir
// (or c9s's own working directory when dir is empty).
func buildNewSessionCmd(ctx ContextConfig, dir string) (*exec.Cmd, error) {
	return buildClaudeCmd(ctx, dir)
}

func buildClaudeCmd(ctx ContextConfig, dir string, args ...string) (*exec.Cmd, error) {
	bin, lookErr := exec.LookPath(ctx.Alias)
	if lookErr != nil {
		bin, lookErr = exec.LookPath("claude")
	}
	if lookErr != nil {
		return nil, fmt.Errorf("neither %q nor \"claude\" found on PATH", ctx.Alias)
	}

	c := exec.Command(bin, args...)
	c.Env = append(os.Environ(), ctx.ResolvedEnv()...)
	if dir != "" {
		c.Dir = dir
	}
	return c, nil
}

// handleNewSession starts a new claude session in the selected project's
// directory (Project View) or the selected session's directory (Session View).
func (m model) handleNewSession() (tea.Model, tea.Cmd) {
	var dir string
	switch m.activeView {
	case viewProjects:
		projects := m.visibleProjects()
		if m.cursor >= len(projects) {
			return m, nil
		}
		dir = projects[m.cursor].Cwd
	case viewSessions:
		sessions := m.visibleSessions()
		if m.cursor >= len(sessions) {
			return m, nil
		}
		dir = sessions[m.cursor].Cwd
	default:
		return m, nil
	}
	ctx := m.config.Contexts[m.config.CurrentContext]

	c, err := buildNewSessionCmd(ctx, dir)
	if err != nil {
		return m, func() tea.Msg {
			return statusMsg("new session failed: " + err.Error())
		}
	}

	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return statusMsg(fmt.Sprintf("new session exited with error: %v", err))
		}
		return statusMsg("new session ended")
	})
}

func (m model) handleResume() (tea.Model, tea.Cmd) {
	if m.activeView != viewSessions {
		return m, nil
	}
	sessions := m.visibleSessions()
	if m.cursor >= len(sessions) {
		return m, nil
	}
	sess := sessions[m.cursor]
	ctx := m.config.Contexts[m.config.CurrentContext]

	c, err := buildResumeCmd(ctx, sess)
	if err != nil {
		return m, func() tea.Msg {
			return statusMsg("resume failed: " + err.Error())
		}
	}

	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return statusMsg(fmt.Sprintf("resume exited with error: %v", err))
		}
		return statusMsg("resumed session " + sess.ID)
	})
}

func (m model) handleRenamePrompt() (tea.Model, tea.Cmd) {
	if m.activeView != viewSessions {
		return m, nil
	}
	sessions := m.visibleSessions()
	if m.cursor >= len(sessions) {
		return m, nil
	}
	sess := sessions[m.cursor]
	m.renaming = true
	m.renamingSession = sess.ID
	m.renameInput = m.sessionNames[sess.ID]
	return m, nil
}

func (m model) handleViewLog() (tea.Model, tea.Cmd) {
	if m.activeView != viewSessions {
		return m, nil
	}
	sessions := m.visibleSessions()
	if m.cursor >= len(sessions) {
		return m, nil
	}
	path := sessions[m.cursor].Path

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
		if _, err := exec.LookPath("vim"); err != nil {
			editor = "nano"
		}
	}

	c := exec.Command(editor, path)
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return statusMsg(fmt.Sprintf("$EDITOR exited with error: %v", err))
		}
		return statusMsg("")
	})
}

func (m model) handleDeletePrompt() (tea.Model, tea.Cmd) {
	if m.activeView != viewSessions {
		return m, nil
	}
	sessions := m.visibleSessions()
	if m.cursor >= len(sessions) {
		return m, nil
	}
	sess := sessions[m.cursor]
	m.confirmDelete = &sess
	return m, nil
}

// statusMsg is a plain string wrapped as a tea.Msg so it can flow back
// through the Update loop after an ExecProcess handoff completes.
type statusMsg string

func (m *model) removeSession(path string) {
	for pi := range m.projects {
		for si, s := range m.projects[pi].Sessions {
			if s.Path == path {
				m.projects[pi].TotalCost -= s.CostUSD
				m.projects[pi].TotalTokens -= s.Tokens
				m.projects[pi].Sessions = append(m.projects[pi].Sessions[:si], m.projects[pi].Sessions[si+1:]...)
				return
			}
		}
	}
}

// ------------------------------------------------------------------
// Helpers: filtering, indexing, row counts
// ------------------------------------------------------------------

func (m model) contextKeys() []string {
	keys := make([]string, 0, len(m.config.Contexts))
	for k := range m.config.Contexts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m model) visibleProjects() []ProjectSummary {
	if m.filterQuery == "" {
		return m.projects
	}
	q := strings.ToLower(m.filterQuery)
	var out []ProjectSummary
	for _, p := range m.projects {
		if strings.Contains(strings.ToLower(p.Cwd), q) {
			out = append(out, p)
		}
	}
	return out
}

func (m model) projectIndex(target ProjectSummary) int {
	for i, p := range m.projects {
		if p.DirName == target.DirName {
			return i
		}
	}
	return -1
}

func (m model) visibleSessions() []SessionSummary {
	var base []SessionSummary
	if m.sessionFilterProj >= 0 && m.sessionFilterProj < len(m.projects) {
		base = m.projects[m.sessionFilterProj].Sessions
	} else {
		base = AllSessions(m.projects)
	}
	if m.filterQuery == "" {
		return base
	}
	q := strings.ToLower(m.filterQuery)
	var out []SessionSummary
	for _, s := range base {
		if strings.Contains(strings.ToLower(s.LastPrompt), q) || strings.Contains(strings.ToLower(s.ID), q) ||
			strings.Contains(strings.ToLower(m.sessionNames[s.ID]), q) {
			out = append(out, s)
		}
	}
	return out
}

func (m model) rowCount() int {
	switch m.activeView {
	case viewContexts:
		return len(m.contextKeys())
	case viewSessions:
		return len(m.visibleSessions())
	case viewQueries:
		return len(m.queries)
	default:
		return len(m.visibleProjects())
	}
}

// ------------------------------------------------------------------
// VIEW
// ------------------------------------------------------------------

var (
	headerStyle = lipgloss.NewStyle().Background(lipgloss.Color("#24292e")).Foreground(lipgloss.Color("#e1e4e8")).Padding(0, 1)
	accentText  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f97583")).Bold(true)
	selectStyle = lipgloss.NewStyle().Background(lipgloss.Color("#0366d6")).Foreground(lipgloss.Color("#ffffff")).Bold(true)
	footerStyle = lipgloss.NewStyle().Background(lipgloss.Color("#1f2428")).Foreground(lipgloss.Color("#6a737d"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6a737d"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")).Bold(true)
)

func formatTokens(n int64) string {
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(s) {
		if i != 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d mins ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

// programOptions are the bubbletea options c9s always runs with. The alternate
// screen is what lets c9s hand the terminal to claude (resume / new session)
// and come back to a clean repaint instead of re-printing below its output.
func programOptions() []tea.ProgramOption {
	return []tea.ProgramOption{tea.WithAltScreen()}
}

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "c9s: %v (continuing with defaults)\n", err)
	}
	sessionNames, err := LoadSessionNames()
	if err != nil {
		fmt.Fprintf(os.Stderr, "c9s: %v (continuing without saved session names)\n", err)
	}

	p := tea.NewProgram(initialModel(cfg, sessionNames), programOptions()...)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "c9s: fatal: %v\n", err)
		os.Exit(1)
	}
}
