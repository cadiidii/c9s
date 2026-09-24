package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

func TestSessionFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initSessionScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features/session_and_project.feature", "features/query_cost.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// sessionWorld holds all scenario-scoped fixture state for both
// session_and_project.feature and query_cost.feature. A fresh instance is
// built in the Before hook so scenarios never leak state into each other.
type sessionWorld struct {
	tmpDir string
	clock  time.Time

	rawLines        []string
	modelTokenAccum map[string]int64
	costModels      map[string]ModelUsage

	sessionPath   string
	parsedSession SessionSummary
	parseErr      error

	baseDir         string
	scannedProjects []ProjectSummary
	scanErr         error

	queries  []QueryCost
	queryErr error

	monthlyProjects []ProjectSummary
	monthlyTokens   int64
	monthlyCost     float64

	// tokensPerAttribution lets the "attributed replies" assertion recover a
	// reply count from QueryCost.Tokens, since QueryCost only stores an
	// aggregate token total per query, not a raw per-reply count.
	tokensPerAttribution int64
}

var sw *sessionWorld

func initSessionScenario(ctx *godog.ScenarioContext) {
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "c9s-session-test-*")
		if err != nil {
			return c, err
		}
		sw = &sessionWorld{
			tmpDir:          dir,
			clock:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			modelTokenAccum: map[string]int64{},
			costModels:      map[string]ModelUsage{},
		}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if sw != nil {
			os.RemoveAll(sw.tmpDir)
		}
		return c, err
	})

	// --- session_and_project.feature ---

	ctx.Step(`^a session transcript with:$`, sessionTranscriptTable)
	ctx.Step(`^the transcript has a final cost-state of \$([0-9.]+) across model "([^"]+)" with (\d+) tokens$`, transcriptCostState)
	ctx.Step(`^a session transcript containing one valid user line and one line of garbage text$`, malformedTranscript)
	ctx.Step(`^I parse the session$`, iParseTheSession)
	ctx.Step(`^parsing the session succeeds$`, parsingSessionSucceeds)
	ctx.Step(`^the session prompt summary is "([^"]*)"$`, theSessionPromptSummaryIs)
	ctx.Step(`^the session has (\d+) exchanges?$`, theSessionHasExchanges)
	ctx.Step(`^the session cost is \$([0-9.]+)$`, theSessionCostIs)
	ctx.Step(`^the session tokens are (\d+)$`, theSessionTokensAre)

	ctx.Step(`^a base directory with no "projects" subdirectory$`, aBaseDirectoryWithNoProjectsSubdirectory)
	ctx.Step(`^I scan projects for that base directory$`, iScanProjects)
	ctx.Step(`^scanning succeeds$`, scanningSucceeds)
	ctx.Step(`^(\d+) projects? (?:is|are) found$`, projectsAreFound)
	ctx.Step(`^a project directory containing (\d+) session files costing \$([0-9.]+) and \$([0-9.]+)$`, projectDirWithSessions)
	ctx.Step(`^the project total cost is \$([0-9.]+)$`, theProjectTotalCostIs)
	ctx.Step(`^the project has (\d+) sessions$`, theProjectHasSessions)

	ctx.Step(`^a project with one session last active this month costing \$([0-9.]+)$`, projectActiveThisMonth)
	ctx.Step(`^a project with one session last active last month costing \$([0-9.]+)$`, projectActiveLastMonth)
	ctx.Step(`^I compute monthly totals for this month$`, iComputeMonthlyTotals)
	ctx.Step(`^the monthly cost is \$([0-9.]+)$`, theMonthlyCostIs)

	// --- query_cost.feature ---

	ctx.Step(`^a session with (\d+) user turns each followed by one assistant reply$`, sessionWithNUserTurns)
	ctx.Step(`^a final cost-state totalling \$([0-9.]+) across model "([^"]+)"$`, finalCostStateAccumulated)
	ctx.Step(`^a session with a user turn that has no assistant reply$`, sessionWithLonelyUserTurn)
	ctx.Step(`^a session with (\d+) user turns, the first followed by (\d+) assistant replies and the second by (\d+)$`, sessionWithTwoGroups)
	ctx.Step(`^the (first|second) query has (\d+) attributed repl(?:y|ies)$`, theNthQueryHasAttributedReplies)
	ctx.Step(`^a final cost-state with model "([^"]+)" costing \$([0-9.]+) for (\d+) tokens$`, finalCostStateExplicit)
	ctx.Step(`^one query used only the haiku model for (\d+) tokens$`, oneQueryUsedHaiku)
	ctx.Step(`^another query used only the opus model for (\d+) tokens$`, anotherQueryUsedOpus)
	ctx.Step(`^I compute per-query costs for the session$`, iComputePerQueryCosts)
	ctx.Step(`^the sum of all per-query costs equals \$([0-9.]+)$`, sumOfPerQueryCostsEquals)
	ctx.Step(`^that query's cost is \$([0-9.]+)$`, thatQueryCostIs)
	ctx.Step(`^the haiku query costs approximately \$([0-9.]+)$`, func(want float64) error {
		return assertQueryCostByModelSubstring("haiku", want)
	})
	ctx.Step(`^the opus query costs approximately \$([0-9.]+)$`, func(want float64) error {
		return assertQueryCostByModelSubstring("opus", want)
	})
}

// ------------------------------------------------------------------
// Fixture-building helpers
// ------------------------------------------------------------------

func (w *sessionWorld) nextTimestamp() string {
	w.clock = w.clock.Add(time.Minute)
	return w.clock.Format(time.RFC3339Nano)
}

func (w *sessionWorld) addLine(m map[string]interface{}) {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err) // fixture construction bug, not a scenario failure - fail loudly
	}
	w.rawLines = append(w.rawLines, string(b))
}

func rawUserLine(content string, isMeta bool, ts string) map[string]interface{} {
	return map[string]interface{}{
		"type": "user", "isMeta": isMeta, "timestamp": ts,
		"message": map[string]interface{}{"role": "user", "content": content},
	}
}

func rawAssistantContentLine(content string, isMeta bool, ts string) map[string]interface{} {
	return map[string]interface{}{
		"type": "assistant", "isMeta": isMeta, "timestamp": ts,
		"message": map[string]interface{}{"role": "assistant", "content": content},
	}
}

func rawAssistantUsageLine(model string, tokens int64, ts string) map[string]interface{} {
	return map[string]interface{}{
		"type": "assistant", "isMeta": false, "timestamp": ts,
		"message": map[string]interface{}{
			"role": "assistant", "model": model,
			"usage": map[string]interface{}{
				"input_tokens": tokens, "output_tokens": 0,
				"cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
				"output_tokens_details": map[string]interface{}{"thinking_tokens": 0},
			},
		},
	}
}

func rawCostStateLine(models map[string]ModelUsage) map[string]interface{} {
	total := 0.0
	mu := map[string]interface{}{}
	for model, m := range models {
		total += m.CostUSD
		mu[model] = map[string]interface{}{
			"inputTokens": m.InputTokens, "outputTokens": m.OutputTokens,
			"thinkingTokens": m.ThinkingTokens, "cacheReadInputTokens": m.CacheReadInputTokens,
			"cacheCreationInputTokens": m.CacheCreationInputTokens, "costUSD": m.CostUSD,
		}
	}
	return map[string]interface{}{"type": "cost-state", "timestamp": sw.nextTimestamp(), "totalCostUSD": total, "modelUsage": mu}
}

func writeRawLines(path string, lines []string) error {
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// ------------------------------------------------------------------
// session_and_project.feature steps
// ------------------------------------------------------------------

func sessionTranscriptTable(table *godog.Table) error {
	if len(table.Rows) < 2 {
		return fmt.Errorf("expected a header row plus at least one data row")
	}
	idx := map[string]int{}
	for i, c := range table.Rows[0].Cells {
		idx[c.Value] = i
	}
	for _, row := range table.Rows[1:] {
		typ := row.Cells[idx["type"]].Value
		content := row.Cells[idx["content"]].Value
		isMeta := row.Cells[idx["isMeta"]].Value == "true"
		ts := sw.nextTimestamp()
		switch typ {
		case "user":
			sw.addLine(rawUserLine(content, isMeta, ts))
		case "assistant":
			sw.addLine(rawAssistantContentLine(content, isMeta, ts))
		default:
			return fmt.Errorf("unsupported transcript row type %q", typ)
		}
	}
	return nil
}

func transcriptCostState(cost float64, model string, tokens int64) error {
	sw.addLine(map[string]interface{}{
		"type": "cost-state", "timestamp": sw.nextTimestamp(),
		"totalCostUSD": cost,
		"modelUsage": map[string]interface{}{
			model: map[string]interface{}{
				"inputTokens": tokens, "outputTokens": 0, "thinkingTokens": 0,
				"cacheReadInputTokens": 0, "cacheCreationInputTokens": 0, "costUSD": cost,
			},
		},
	})
	return nil
}

func malformedTranscript() error {
	sw.addLine(rawUserLine("hello", false, sw.nextTimestamp()))
	sw.rawLines = append(sw.rawLines, "not valid json {{{")
	return nil
}

func iParseTheSession() error {
	path := filepath.Join(sw.tmpDir, "session.jsonl")
	if err := writeRawLines(path, sw.rawLines); err != nil {
		return err
	}
	sw.sessionPath = path
	sw.parsedSession, sw.parseErr = ParseSession(path)
	return nil
}

func parsingSessionSucceeds() error {
	if sw.parseErr != nil {
		return fmt.Errorf("expected no error parsing session, got %v", sw.parseErr)
	}
	return nil
}

func theSessionPromptSummaryIs(want string) error {
	if sw.parsedSession.PromptSummary != want {
		return fmt.Errorf("prompt summary = %q, want %q", sw.parsedSession.PromptSummary, want)
	}
	return nil
}

func theSessionHasExchanges(n int) error {
	if sw.parsedSession.Exchanges != n {
		return fmt.Errorf("exchanges = %d, want %d", sw.parsedSession.Exchanges, n)
	}
	return nil
}

func theSessionCostIs(want float64) error {
	if math.Abs(sw.parsedSession.CostUSD-want) > 0.001 {
		return fmt.Errorf("session cost = %.4f, want %.4f", sw.parsedSession.CostUSD, want)
	}
	return nil
}

func theSessionTokensAre(want int64) error {
	if sw.parsedSession.Tokens != want {
		return fmt.Errorf("session tokens = %d, want %d", sw.parsedSession.Tokens, want)
	}
	return nil
}

func aBaseDirectoryWithNoProjectsSubdirectory() error {
	sw.baseDir = sw.tmpDir
	return nil
}

func iScanProjects() error {
	sw.scannedProjects, sw.scanErr = ScanProjects(sw.baseDir)
	return nil
}

func scanningSucceeds() error {
	if sw.scanErr != nil {
		return fmt.Errorf("expected no error scanning projects, got %v", sw.scanErr)
	}
	return nil
}

func projectsAreFound(n int) error {
	if len(sw.scannedProjects) != n {
		return fmt.Errorf("found %d projects, want %d", len(sw.scannedProjects), n)
	}
	return nil
}

func projectDirWithSessions(n int, cost1, cost2 float64) error {
	dir := filepath.Join(sw.tmpDir, "projects", "testproject")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	costs := []float64{cost1, cost2}
	for i := 0; i < n && i < len(costs); i++ {
		ts := sw.nextTimestamp()
		lines := []string{}
		u, _ := json.Marshal(rawUserLine("hi", false, ts))
		cs, _ := json.Marshal(map[string]interface{}{
			"type": "cost-state", "timestamp": ts, "totalCostUSD": costs[i],
			"modelUsage": map[string]interface{}{
				"claude-sonnet-5": map[string]interface{}{
					"inputTokens": 10, "outputTokens": 10, "thinkingTokens": 0,
					"cacheReadInputTokens": 0, "cacheCreationInputTokens": 0, "costUSD": costs[i],
				},
			},
		})
		lines = append(lines, string(u), string(cs))
		path := filepath.Join(dir, fmt.Sprintf("session-%d.jsonl", i))
		if err := writeRawLines(path, lines); err != nil {
			return err
		}
	}
	sw.baseDir = sw.tmpDir
	return nil
}

func theProjectTotalCostIs(want float64) error {
	if len(sw.scannedProjects) == 0 {
		return fmt.Errorf("no projects scanned")
	}
	got := sw.scannedProjects[0].TotalCost
	if math.Abs(got-want) > 0.001 {
		return fmt.Errorf("project total cost = %.4f, want %.4f", got, want)
	}
	return nil
}

func theProjectHasSessions(n int) error {
	if len(sw.scannedProjects) == 0 {
		return fmt.Errorf("no projects scanned")
	}
	if got := len(sw.scannedProjects[0].Sessions); got != n {
		return fmt.Errorf("project has %d sessions, want %d", got, n)
	}
	return nil
}

func projectActiveThisMonth(cost float64) error {
	sw.monthlyProjects = append(sw.monthlyProjects, ProjectSummary{
		Sessions: []SessionSummary{{CostUSD: cost, LastActive: time.Now()}},
	})
	return nil
}

func projectActiveLastMonth(cost float64) error {
	sw.monthlyProjects = append(sw.monthlyProjects, ProjectSummary{
		Sessions: []SessionSummary{{CostUSD: cost, LastActive: time.Now().AddDate(0, -1, 0)}},
	})
	return nil
}

func iComputeMonthlyTotals() error {
	sw.monthlyTokens, sw.monthlyCost = MonthlyTotals(sw.monthlyProjects, time.Now())
	return nil
}

func theMonthlyCostIs(want float64) error {
	if math.Abs(sw.monthlyCost-want) > 0.001 {
		return fmt.Errorf("monthly cost = %.4f, want %.4f", sw.monthlyCost, want)
	}
	return nil
}

// ------------------------------------------------------------------
// query_cost.feature steps
// ------------------------------------------------------------------

const testTokensPerReply = int64(100)

func sessionWithNUserTurns(n int) error {
	for i := 0; i < n; i++ {
		sw.addLine(rawUserLine(fmt.Sprintf("query %d", i+1), false, sw.nextTimestamp()))
		sw.addLine(rawAssistantUsageLine("claude-sonnet-5", testTokensPerReply, sw.nextTimestamp()))
		sw.modelTokenAccum["claude-sonnet-5"] += testTokensPerReply
	}
	return nil
}

func finalCostStateAccumulated(cost float64, model string) error {
	tokens := sw.modelTokenAccum[model]
	sw.costModels[model] = ModelUsage{InputTokens: tokens, CostUSD: cost}
	return nil
}

func sessionWithLonelyUserTurn() error {
	sw.addLine(rawUserLine("lonely query", false, sw.nextTimestamp()))
	return nil
}

func sessionWithTwoGroups(_ int, firstN, secondN int) error {
	sw.addLine(rawUserLine("query 1", false, sw.nextTimestamp()))
	for i := 0; i < firstN; i++ {
		sw.addLine(rawAssistantUsageLine("claude-sonnet-5", testTokensPerReply, sw.nextTimestamp()))
		sw.modelTokenAccum["claude-sonnet-5"] += testTokensPerReply
	}
	sw.addLine(rawUserLine("query 2", false, sw.nextTimestamp()))
	for i := 0; i < secondN; i++ {
		sw.addLine(rawAssistantUsageLine("claude-sonnet-5", testTokensPerReply, sw.nextTimestamp()))
		sw.modelTokenAccum["claude-sonnet-5"] += testTokensPerReply
	}
	sw.tokensPerAttribution = testTokensPerReply
	return nil
}

// theNthQueryHasAttributedReplies infers a reply count from QueryCost.Tokens,
// since QueryCost only stores an aggregate token total per query rather than
// a raw per-reply count: every synthetic reply in this scenario carries the
// same fixed token count, so tokens / tokensPerReply recovers the count.
func theNthQueryHasAttributedReplies(which string, n int) error {
	idx := 0
	if which == "second" {
		idx = 1
	}
	if idx >= len(sw.queries) {
		return fmt.Errorf("no query at index %d (have %d)", idx, len(sw.queries))
	}
	if sw.tokensPerAttribution == 0 {
		return fmt.Errorf("tokensPerAttribution not set by a prior step")
	}
	got := sw.queries[idx].Tokens / sw.tokensPerAttribution
	if int(got) != n {
		return fmt.Errorf("%s query has %d attributed replies (tokens=%d), want %d", which, got, sw.queries[idx].Tokens, n)
	}
	return nil
}

func finalCostStateExplicit(model string, cost float64, tokens int64) error {
	sw.costModels[model] = ModelUsage{InputTokens: tokens, CostUSD: cost}
	return nil
}

func oneQueryUsedHaiku(tokens int64) error {
	sw.addLine(rawUserLine("haiku query", false, sw.nextTimestamp()))
	sw.addLine(rawAssistantUsageLine("claude-haiku-4-5-20251001", tokens, sw.nextTimestamp()))
	return nil
}

func anotherQueryUsedOpus(tokens int64) error {
	sw.addLine(rawUserLine("opus query", false, sw.nextTimestamp()))
	sw.addLine(rawAssistantUsageLine("claude-opus-5-5", tokens, sw.nextTimestamp()))
	return nil
}

func iComputePerQueryCosts() error {
	if len(sw.costModels) > 0 {
		sw.addLine(rawCostStateLine(sw.costModels))
	}
	path := filepath.Join(sw.tmpDir, "session.jsonl")
	if err := writeRawLines(path, sw.rawLines); err != nil {
		return err
	}
	sw.queries, sw.queryErr = ParseSessionQueries(path)
	return nil
}

func sumOfPerQueryCostsEquals(want float64) error {
	if sw.queryErr != nil {
		return fmt.Errorf("expected no error computing query costs, got %v", sw.queryErr)
	}
	sum := 0.0
	for _, q := range sw.queries {
		sum += q.CostUSD
	}
	if math.Abs(sum-want) > 0.01 {
		return fmt.Errorf("sum of per-query costs = %.4f, want %.4f", sum, want)
	}
	return nil
}

func thatQueryCostIs(want float64) error {
	if len(sw.queries) == 0 {
		return fmt.Errorf("no queries found")
	}
	got := sw.queries[0].CostUSD
	if math.Abs(got-want) > 0.01 {
		return fmt.Errorf("query cost = %.4f, want %.4f", got, want)
	}
	return nil
}

func assertQueryCostByModelSubstring(sub string, want float64) error {
	for _, q := range sw.queries {
		for _, m := range q.Models {
			if strings.Contains(m, sub) {
				if math.Abs(q.CostUSD-want) > 0.01 {
					return fmt.Errorf("%s query cost = %.4f, want ~%.4f", sub, q.CostUSD, want)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("no query found using a model containing %q", sub)
}
