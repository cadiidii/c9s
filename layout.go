package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// chromeLines is the fixed vertical overhead around a table's body rows:
// 2 header lines, 1 blank line, the column-title line, its separator, and
// the 1-line footer.
const chromeLines = 6

// column describes one table column. flex > 0 columns share whatever width
// the fixed columns leave over, in proportion to flex, within [min, max]
// (max 0 = unbounded).
type column struct {
	title string
	width int
	flex  int
	min   int
	max   int
}

// bodyRows is how many table rows fit on screen (unbounded before the first
// WindowSizeMsg, so the model behaves as before in headless use).
func (m model) bodyRows() int {
	if m.height == 0 {
		return 1 << 30
	}
	if n := m.height - chromeLines; n > 1 {
		return n
	}
	return 1
}

// clampScroll keeps the selected row inside the visible window.
func (m *model) clampScroll() {
	vis := m.bodyRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+vis {
		m.offset = m.cursor - vis + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// cell truncates s to w display columns (with an ellipsis) and pads it out.
func cell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// resolveWidths turns column specs into concrete widths for a terminal of the
// given width (0 = use each flex column's min, as before any size is known).
func resolveWidths(cols []column, width int) []int {
	widths := make([]int, len(cols))
	used, totalFlex := len(cols)-1, 0 // separators
	for i, c := range cols {
		if c.flex == 0 {
			widths[i] = c.width
			used += c.width
		} else {
			totalFlex += c.flex
		}
	}
	remaining := width - used
	for i, c := range cols {
		if c.flex == 0 {
			continue
		}
		w := c.min
		if width > 0 {
			w = remaining * c.flex / totalFlex
		}
		if w < c.min {
			w = c.min
		}
		if c.max > 0 && w > c.max {
			w = c.max
		}
		widths[i] = w
	}
	// A capped column frees space; give it to the last uncapped flex column.
	if width > 0 {
		sum := len(cols) - 1
		for _, w := range widths {
			sum += w
		}
		for i := len(cols) - 1; i >= 0 && sum < width; i-- {
			if cols[i].flex > 0 && cols[i].max == 0 {
				widths[i] += width - sum
				break
			}
		}
	}
	return widths
}

func joinRow(cells []string, widths []int) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		v := ""
		if i < len(cells) {
			v = cells[i]
		}
		parts[i] = cell(v, w)
	}
	return strings.Join(parts, " ")
}

// renderTable renders the title line, separator and the visible slice of rows,
// highlighting the selected row across the full width.
func (m model) renderTable(cols []column, rows [][]string) []string {
	widths := resolveWidths(cols, m.width)
	titles := make([]string, len(cols))
	for i, c := range cols {
		titles[i] = c.title
	}
	out := []string{joinRow(titles, widths)}
	total := len(widths) - 1
	for _, w := range widths {
		total += w
	}
	out = append(out, strings.Repeat("-", total))

	end := m.offset + m.bodyRows()
	if end > len(rows) {
		end = len(rows)
	}
	for i := m.offset; i < end; i++ {
		line := joinRow(rows[i], widths)
		if i == m.cursor {
			line = selectStyle.Render(line)
		}
		out = append(out, line)
	}
	return out
}

func (m model) View() string {
	width := m.width
	body := m.bodyLines()

	lines := append(m.renderHeader(), "")
	lines = append(lines, body...)

	footer := m.renderFooter()
	if m.height > 0 {
		for len(lines) < m.height-1 {
			lines = append(lines, "")
		}
		if len(lines) > m.height-1 {
			lines = lines[:m.height-1]
		}
	}
	lines = append(lines, footer)

	if width > 0 {
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, width, "")
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) bodyLines() []string {
	switch {
	case m.confirmDelete != nil:
		return []string{errorStyle.Render(fmt.Sprintf("Are you sure you want to delete session %s? (y/n)", m.confirmDelete.ID))}
	case m.loading:
		return []string{dimStyle.Render("Scanning project history...")}
	case m.loadErr != nil:
		return []string{errorStyle.Render(fmt.Sprintf("Error scanning history: %v", m.loadErr))}
	case m.activeView == viewQueries && m.queriesLoading:
		return []string{dimStyle.Render("Scanning session for per-query costs...")}
	case m.activeView == viewQueries && m.queriesErr != nil:
		return []string{errorStyle.Render(fmt.Sprintf("Error scanning session: %v", m.queriesErr))}
	}
	switch m.activeView {
	case viewContexts:
		return m.renderContexts()
	case viewSessions:
		return m.renderSessions()
	case viewQueries:
		return m.renderQueries()
	default:
		return m.renderProjects()
	}
}

func (m model) renderHeader() []string {
	ctx := m.config.Contexts[m.config.CurrentContext]
	tokens, cost := MonthlyTotals(m.projects, time.Now())
	line1 := fmt.Sprintf("Context: [%s] (%s)  |  Base: %s/projects/", accentText.Render(m.config.CurrentContext), ctx.Alias, ctx.BaseDir)
	line2 := fmt.Sprintf("Tokens (Month): %s      |  Estimated Spend: $%.2f", formatTokens(tokens), cost)
	return []string{m.bar(headerStyle, line1), m.bar(headerStyle, line2)}
}

// bar renders a full-width styled line (padding included) that never wraps.
func (m model) bar(style lipgloss.Style, text string) string {
	if m.width == 0 {
		return style.Render(text)
	}
	inner := m.width - style.GetHorizontalFrameSize()
	if inner < 1 {
		inner = 1
	}
	return style.Width(m.width).Render(ansi.Truncate(text, inner, "…"))
}

func (m model) renderContexts() []string {
	cols := []column{
		{title: "CONTEXT KEY", width: 15},
		{title: "SHELL ALIAS", width: 20},
		{title: "BASE DIR", flex: 1, min: 10},
		{title: "STATUS", width: 8},
	}
	var rows [][]string
	for _, key := range m.contextKeys() {
		ctx := m.config.Contexts[key]
		status := ""
		if key == m.config.CurrentContext {
			status = "(active)"
		}
		rows = append(rows, []string{key, ctx.Alias, ctx.BaseDir, status})
	}
	return m.renderTable(cols, rows)
}

func (m model) renderProjects() []string {
	projects := m.visibleProjects()
	if len(projects) == 0 {
		return []string{dimStyle.Render("No project history found for this context yet.")}
	}
	cols := []column{
		{title: "PROJECT WORKSPACE PATH", flex: 1, min: 20},
		{title: "SESSIONS", width: 8},
		{title: "LAST ACTIVE", width: 14},
		{title: "COST ($)", width: 10},
	}
	var rows [][]string
	for _, p := range projects {
		rows = append(rows, []string{p.Cwd, fmt.Sprint(len(p.Sessions)), relTime(p.LastActive), fmt.Sprintf("$%.2f", p.TotalCost)})
	}
	return m.renderTable(cols, rows)
}

func (m model) renderSessions() []string {
	sessions := m.visibleSessions()
	if len(sessions) == 0 {
		return []string{dimStyle.Render("No sessions found.")}
	}
	global := m.sessionFilterProj < 0
	cols := []column{
		{title: "SESSION ID / TIME", width: 20},
		{title: "NAME", flex: 3, min: 12, max: 30},
	}
	if global {
		cols = append(cols, column{title: "PROJECT", flex: 4, min: 14, max: 45})
	}
	cols = append(cols,
		column{title: "LAST PROMPT", flex: 7, min: 20},
		column{title: "EXCHNG", width: 6},
		column{title: "TOKENS", width: 8},
	)
	var rows [][]string
	for _, sess := range sessions {
		name := m.sessionName(sess)
		if name == "" {
			name = "-"
		}
		id := sess.ID
		if len(id) > 8 {
			id = id[:8]
		}
		row := []string{fmt.Sprintf("%s (%s)", id, relTime(sess.LastActive)), name}
		if global {
			row = append(row, sessionProject(sess))
		}
		rows = append(rows, append(row, sess.LastPrompt, fmt.Sprint(sess.Exchanges), formatTokens(sess.Tokens)))
	}
	return m.renderTable(cols, rows)
}

// sessionProject is the project a session belongs to, for the all-sessions list.
func sessionProject(s SessionSummary) string {
	if s.Cwd != "" {
		return s.Cwd
	}
	return s.ProjectDir
}

func (m model) renderQueries() []string {
	if len(m.queries) == 0 {
		return []string{dimStyle.Render("No queries found for this session.")}
	}
	cols := []column{
		{title: "QUERY #", width: 7},
		{title: "TIME", width: 14},
		{title: "MODEL(S)", width: 20},
		{title: "TOKENS", width: 10},
		{title: "COST ($)", width: 9},
		{title: "PROMPT", flex: 1, min: 20},
	}
	var rows [][]string
	for _, q := range m.queries {
		rows = append(rows, []string{
			fmt.Sprint(q.Index), relTime(q.Timestamp), modelsLabel(q.Models),
			formatTokens(q.Tokens), fmt.Sprintf("$%.2f", q.CostUSD), q.PromptSummary,
		})
	}
	return m.renderTable(cols, rows)
}

func (m model) renderFooter() string {
	if m.renaming {
		return m.bar(footerStyle, "rename: "+m.renameInput)
	}
	if m.inCommand {
		return m.bar(footerStyle, ":"+m.commandInput)
	}
	if m.filtering || m.filterQuery != "" {
		return m.bar(footerStyle, "/"+m.filterQuery)
	}

	var legend string
	switch m.activeView {
	case viewSessions:
		legend = " <Enter> Costs <s> Projects <n> New <r> Resume <v> Log <R> Rename <d> Del <Esc> Up <:> Cmd <q> Quit "
	case viewContexts:
		legend = " <Enter> Switch Context  <:> Cmd  <q> Quit "
	case viewQueries:
		legend = " <Esc> Back  <:> Cmd  <q> Quit "
	default:
		legend = " <Enter> Sessions  <s> Sessions  <n> New  <j/k> Move  </> Filter  <:> Cmd  <q> Quit "
	}
	if m.statusMsg != "" {
		legend = fmt.Sprintf(" [%s] |%s", m.statusMsg, legend)
	}
	return m.bar(footerStyle, legend)
}
