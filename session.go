package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ModelUsage mirrors the per-model usage block inside a "cost-state" line.
type ModelUsage struct {
	InputTokens              int64   `json:"inputTokens"`
	OutputTokens             int64   `json:"outputTokens"`
	ThinkingTokens           int64   `json:"thinkingTokens"`
	CacheReadInputTokens     int64   `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64   `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
}

func (m ModelUsage) totalTokens() int64 {
	return m.InputTokens + m.OutputTokens + m.ThinkingTokens + m.CacheReadInputTokens + m.CacheCreationInputTokens
}

// jsonlLine is a superset decoder for the handful of record shapes we care
// about across the many "type" variants Claude Code writes to a transcript.
type jsonlLine struct {
	Type         string                `json:"type"`
	Timestamp    string                `json:"timestamp"`
	Cwd          string                `json:"cwd"`
	GitBranch    string                `json:"gitBranch"`
	IsMeta       bool                  `json:"isMeta"`
	TotalCostUSD *float64              `json:"totalCostUSD"`
	ModelUsage   map[string]ModelUsage `json:"modelUsage"`
	Message      *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// SessionSummary is the per-session row shown in the Session View.
type SessionSummary struct {
	ID            string
	Path          string
	ProjectDir    string
	Cwd           string
	GitBranch     string
	PromptSummary string
	Exchanges     int
	Tokens        int64
	CostUSD       float64
	LastActive    time.Time
}

// ProjectSummary is the per-project row shown in the Project View, aggregated
// across every session file found in that project's directory.
type ProjectSummary struct {
	Cwd         string
	DirName     string
	GitBranch   string
	Sessions    []SessionSummary
	LastActive  time.Time
	TotalCost   float64
	TotalTokens int64
}

// ParseSession reads one <session-id>.jsonl file and extracts summary stats.
// Malformed individual lines are skipped rather than failing the whole file,
// since a crashed or force-killed session can leave a truncated last line.
func ParseSession(path string) (SessionSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionSummary{}, err
	}
	defer f.Close()

	sess := SessionSummary{
		ID:   strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path: path,
	}

	scanner := bufio.NewScanner(f)
	// Assistant lines can carry large "thinking" blocks well past the
	// default 64KB token limit, so give the scanner a generous buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var line jsonlLine
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}

		if line.Cwd != "" {
			sess.Cwd = line.Cwd
		}
		if line.GitBranch != "" {
			sess.GitBranch = line.GitBranch
		}
		if ts, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil && ts.After(sess.LastActive) {
			sess.LastActive = ts
		}

		switch line.Type {
		case "user", "assistant":
			if line.IsMeta {
				continue
			}
			sess.Exchanges++
			if line.Type == "user" && sess.PromptSummary == "" && line.Message != nil {
				if text := extractText(line.Message.Content); text != "" {
					sess.PromptSummary = truncate(text, 80)
				}
			}
		case "cost-state":
			// cost-state lines carry cumulative totals, so the last one
			// encountered in the file wins.
			if line.TotalCostUSD != nil {
				sess.CostUSD = *line.TotalCostUSD
			}
			var tokens int64
			for _, mu := range line.ModelUsage {
				tokens += mu.totalTokens()
			}
			if len(line.ModelUsage) > 0 {
				sess.Tokens = tokens
			}
		}
	}

	if sess.LastActive.IsZero() {
		if info, statErr := f.Stat(); statErr == nil {
			sess.LastActive = info.ModTime()
		}
	}

	return sess, scanner.Err()
}

// extractText pulls a display string out of a message's content field, which
// Claude Code writes either as a bare string or as an array of typed blocks.
func extractText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// ScanProjects walks <baseDir>/projects and returns one ProjectSummary per
// subdirectory, each aggregated from every *.jsonl session file inside it.
// A missing projects directory is treated as an empty result, not an error,
// so a fresh context shows an empty-state banner instead of crashing.
func ScanProjects(baseDir string) ([]ProjectSummary, error) {
	projectsDir := filepath.Join(baseDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var projects []ProjectSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirPath := filepath.Join(projectsDir, e.Name())
		files, err := filepath.Glob(filepath.Join(dirPath, "*.jsonl"))
		if err != nil || len(files) == 0 {
			continue
		}

		proj := ProjectSummary{DirName: e.Name()}
		for _, path := range files {
			sess, err := ParseSession(path)
			if err != nil {
				continue
			}
			sess.ProjectDir = e.Name()
			proj.Sessions = append(proj.Sessions, sess)
			proj.TotalCost += sess.CostUSD
			proj.TotalTokens += sess.Tokens
			if sess.LastActive.After(proj.LastActive) {
				proj.LastActive = sess.LastActive
				if sess.GitBranch != "" {
					proj.GitBranch = sess.GitBranch
				}
			}
			if proj.Cwd == "" && sess.Cwd != "" {
				proj.Cwd = sess.Cwd
			}
		}
		if len(proj.Sessions) == 0 {
			continue
		}
		sort.Slice(proj.Sessions, func(i, j int) bool {
			return proj.Sessions[i].LastActive.After(proj.Sessions[j].LastActive)
		})
		// Falls back to the raw (still "-"-joined) directory name on the rare
		// session that never recorded a cwd - ambiguous to decode since real
		// path segments can themselves contain hyphens, so we don't guess.
		if proj.Cwd == "" {
			proj.Cwd = proj.DirName
		}
		projects = append(projects, proj)
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].LastActive.After(projects[j].LastActive)
	})
	return projects, nil
}

// MonthlyTotals sums cost/tokens across sessions last active in ref's month,
// for the header's "Tokens (Month)" / "Estimated Spend" figures.
func MonthlyTotals(projects []ProjectSummary, ref time.Time) (tokens int64, costUSD float64) {
	y, m, _ := ref.Date()
	for _, p := range projects {
		for _, s := range p.Sessions {
			sy, sm, _ := s.LastActive.Date()
			if sy == y && sm == m {
				tokens += s.Tokens
				costUSD += s.CostUSD
			}
		}
	}
	return
}

// AllSessions flattens every project's sessions into one chronological list,
// backing the global ":sess" timeline view.
func AllSessions(projects []ProjectSummary) []SessionSummary {
	var all []SessionSummary
	for _, p := range projects {
		all = append(all, p.Sessions...)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].LastActive.After(all[j].LastActive)
	})
	return all
}
