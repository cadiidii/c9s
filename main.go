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
	switch msg := msg.(type) {
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
	bin, lookErr := exec.LookPath(ctx.Alias)
	if lookErr != nil {
		bin, lookErr = exec.LookPath("claude")
	}
	if lookErr != nil {
		return nil, fmt.Errorf("neither %q nor \"claude\" found on PATH", ctx.Alias)
	}

	c := exec.Command(bin, "--resume", sess.ID)
	c.Env = append(os.Environ(), ctx.ResolvedEnv()...)
	if sess.Cwd != "" {
		c.Dir = sess.Cwd
	}
	return c, nil
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
		if strings.Contains(strings.ToLower(s.PromptSummary), q) || strings.Contains(strings.ToLower(s.ID), q) {
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

func (m model) View() string {
	var s strings.Builder

	s.WriteString(m.renderHeader() + "\n\n")

	if m.confirmDelete != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("Are you sure you want to delete session %s? (y/n)", m.confirmDelete.ID)) + "\n")
	} else if m.loading {
		s.WriteString(dimStyle.Render("Scanning project history...") + "\n")
	} else if m.loadErr != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("Error scanning history: %v", m.loadErr)) + "\n")
	} else if m.activeView == viewQueries && m.queriesLoading {
		s.WriteString(dimStyle.Render("Scanning session for per-query costs...") + "\n")
	} else if m.activeView == viewQueries && m.queriesErr != nil {
		s.WriteString(errorStyle.Render(fmt.Sprintf("Error scanning session: %v", m.queriesErr)) + "\n")
	} else {
		switch m.activeView {
		case viewContexts:
			s.WriteString(m.renderContexts())
		case viewSessions:
			s.WriteString(m.renderSessions())
		case viewQueries:
			s.WriteString(m.renderQueries())
		default:
			s.WriteString(m.renderProjects())
		}
	}

	s.WriteString("\n")
	s.WriteString(m.renderFooter())
	return s.String()
}

func (m model) renderHeader() string {
	ctx := m.config.Contexts[m.config.CurrentContext]
	tokens, cost := MonthlyTotals(m.projects, time.Now())
	line1 := fmt.Sprintf("Context: [%s] (%s)  |  Base: %s/projects/", accentText.Render(m.config.CurrentContext), ctx.Alias, ctx.BaseDir)
	line2 := fmt.Sprintf("Tokens (Month): %s      |  Estimated Spend: $%.2f", formatTokens(tokens), cost)
	return headerStyle.Render(line1 + "\n" + line2)
}

func (m model) renderContexts() string {
	var s strings.Builder
	s.WriteString(fmt.Sprintf("%-15s %-20s %-25s\n", "CONTEXT KEY", "SHELL ALIAS", "BASE DIR"))
	s.WriteString(strings.Repeat("-", 62) + "\n")
	for i, key := range m.contextKeys() {
		ctx := m.config.Contexts[key]
		row := fmt.Sprintf("%-15s %-20s %-25s", key, ctx.Alias, ctx.BaseDir)
		if key == m.config.CurrentContext {
			row += " (active)"
		}
		s.WriteString(m.styledRow(row, i) + "\n")
	}
	return s.String()
}

func (m model) renderProjects() string {
	rows := m.visibleProjects()
	if len(rows) == 0 {
		return dimStyle.Render("No project history found for this context yet.") + "\n"
	}
	var s strings.Builder
	s.WriteString(fmt.Sprintf("%-55s %-9s %-14s %-10s\n", "PROJECT WORKSPACE PATH", "SESSIONS", "LAST ACTIVE", "COST ($)"))
	s.WriteString(strings.Repeat("-", 92) + "\n")
	for i, p := range rows {
		row := fmt.Sprintf("%-55s %-9d %-14s $%-9.2f", truncate(p.Cwd, 55), len(p.Sessions), relTime(p.LastActive), p.TotalCost)
		s.WriteString(m.styledRow(row, i) + "\n")
	}
	return s.String()
}

func (m model) renderSessions() string {
	rows := m.visibleSessions()
	if len(rows) == 0 {
		return dimStyle.Render("No sessions found.") + "\n"
	}
	var s strings.Builder
	s.WriteString(fmt.Sprintf("%-38s %-40s %-9s %-10s\n", "SESSION ID / TIME", "INITIAL PROMPT", "EXCHNG", "TOKENS"))
	s.WriteString(strings.Repeat("-", 100) + "\n")
	for i, sess := range rows {
		idCol := fmt.Sprintf("%s (%s)", sess.ID[:8], relTime(sess.LastActive))
		label := sess.PromptSummary
		if custom, ok := m.sessionNames[sess.ID]; ok && custom != "" {
			label = "★ " + custom // filled star marks a custom name
		}
		row := fmt.Sprintf("%-38s %-40s %-9d %-10s", idCol, truncate(label, 40), sess.Exchanges, formatTokens(sess.Tokens))
		s.WriteString(m.styledRow(row, i) + "\n")
	}
	return s.String()
}

func (m model) renderQueries() string {
	if len(m.queries) == 0 {
		return dimStyle.Render("No queries found for this session.") + "\n"
	}
	var s strings.Builder
	s.WriteString(fmt.Sprintf("%-8s %-14s %-20s %-10s %-9s %-40s\n", "QUERY #", "TIME", "MODEL(S)", "TOKENS", "COST ($)", "PROMPT"))
	s.WriteString(strings.Repeat("-", 104) + "\n")
	for i, q := range m.queries {
		row := fmt.Sprintf("%-8d %-14s %-20s %-10s $%-8.2f %-40s",
			q.Index, relTime(q.Timestamp), truncate(modelsLabel(q.Models), 20), formatTokens(q.Tokens), q.CostUSD, truncate(q.PromptSummary, 40))
		s.WriteString(m.styledRow(row, i) + "\n")
	}
	return s.String()
}

func (m model) styledRow(row string, i int) string {
	if m.cursor == i {
		return selectStyle.Render(row)
	}
	return row
}

func (m model) renderFooter() string {
	if m.renaming {
		return footerStyle.Render("rename: " + m.renameInput)
	}
	if m.inCommand {
		return footerStyle.Render(":" + m.commandInput)
	}
	if m.filtering || m.filterQuery != "" {
		return footerStyle.Render("/" + m.filterQuery)
	}

	var legend string
	switch m.activeView {
	case viewSessions:
		legend = " <Enter> Query Costs  <r> Resume  <v> View Log  <n> Rename  <d> Delete  <Esc> Back  <:> Cmd  <q> Quit "
	case viewContexts:
		legend = " <Enter> Switch Context  <:> Cmd  <q> Quit "
	case viewQueries:
		legend = " <Esc> Back  <:> Cmd  <q> Quit "
	default:
		legend = " <Enter> Sessions  <j/k> Move  </> Filter  <:> Cmd  <q> Quit "
	}
	if m.statusMsg != "" {
		legend = fmt.Sprintf(" [%s] |%s", m.statusMsg, legend)
	}
	return footerStyle.Render(legend)
}

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

func main() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "c9s: %v (continuing with defaults)\n", err)
	}
	sessionNames, err := LoadSessionNames()
	if err != nil {
		fmt.Fprintf(os.Stderr, "c9s: %v (continuing without saved session names)\n", err)
	}

	p := tea.NewProgram(initialModel(cfg, sessionNames))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "c9s: fatal: %v\n", err)
		os.Exit(1)
	}
}
