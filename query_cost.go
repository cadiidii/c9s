package main

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
)

// QueryCost is one user-prompt-to-next-user-prompt exchange within a session,
// with an estimated cost derived from the session's own authoritative total.
type QueryCost struct {
	Index         int
	Timestamp     time.Time
	PromptSummary string
	Models        []string
	Tokens        int64
	CostUSD       float64
}

// usageBlock mirrors the per-turn "usage" object on an assistant message.
type usageBlock struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokensDetails      *struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

func (u *usageBlock) totalTokens() int64 {
	tokens := u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	if u.OutputTokensDetails != nil {
		tokens += u.OutputTokensDetails.ThinkingTokens
	}
	return tokens
}

// queryLine decodes only the fields needed to attribute per-turn token usage
// to a model and a query group; kept separate from jsonlLine so the cheap
// project/session listing scan doesn't pay for decoding message.usage.
type queryLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	IsMeta    bool   `json:"isMeta"`
	Message   *struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *usageBlock     `json:"usage"`
	} `json:"message"`
	TotalCostUSD *float64              `json:"totalCostUSD"`
	ModelUsage   map[string]ModelUsage `json:"modelUsage"`
}

// ParseSessionQueries re-reads a session file and buckets it into per-query
// cost estimates. There's no per-turn cost in the transcript - only sparse
// cumulative "cost-state" snapshots - so this derives an effective $/token
// rate per model from the session's *final* cost-state totals (costUSD ÷
// tokens, per model), applies that rate to each turn's own usage block, then
// rescales every query proportionally so the sum always ties out exactly to
// the session's authoritative total cost. That avoids hardcoding a separate
// price table (which would drift from Anthropic's actual pricing over time)
// at the cost of blending cache-read/write/input into one rate per model
// rather than pricing each token type separately.
func ParseSessionQueries(path string) ([]QueryCost, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	type turnUsage struct {
		model  string
		tokens int64
	}
	type group struct {
		timestamp     time.Time
		promptSummary string
		turns         []turnUsage
	}

	var groups []group
	finalModelUsage := map[string]ModelUsage{}

	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var line queryLine
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}
		if line.IsMeta {
			continue
		}

		switch line.Type {
		case "user":
			ts, _ := time.Parse(time.RFC3339Nano, line.Timestamp)
			g := group{timestamp: ts}
			if line.Message != nil {
				g.promptSummary = truncate(extractText(line.Message.Content), 60)
			}
			groups = append(groups, g)

		case "assistant":
			if line.Message == nil || line.Message.Usage == nil || len(groups) == 0 {
				continue
			}
			cur := &groups[len(groups)-1]
			cur.turns = append(cur.turns, turnUsage{
				model:  line.Message.Model,
				tokens: line.Message.Usage.totalTokens(),
			})

		case "cost-state":
			// Cumulative snapshot; the last one in the file has the final totals.
			for model, mu := range line.ModelUsage {
				finalModelUsage[model] = mu
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	rate := make(map[string]float64, len(finalModelUsage))
	actualTotal := 0.0
	for model, mu := range finalModelUsage {
		actualTotal += mu.CostUSD
		if tokens := mu.totalTokens(); tokens > 0 {
			rate[model] = mu.CostUSD / float64(tokens)
		}
	}

	results := make([]QueryCost, 0, len(groups))
	rawTotal := 0.0
	for i, g := range groups {
		if len(g.turns) == 0 && g.promptSummary == "" {
			continue // meta-only user line (e.g. local-command caveat) with no assistant reply
		}
		modelSet := map[string]bool{}
		var tokens int64
		var rawCost float64
		for _, t := range g.turns {
			tokens += t.tokens
			rawCost += float64(t.tokens) * rate[t.model]
			if t.model != "" {
				modelSet[t.model] = true
			}
		}
		models := make([]string, 0, len(modelSet))
		for m := range modelSet {
			models = append(models, m)
		}
		sort.Strings(models)

		rawTotal += rawCost
		results = append(results, QueryCost{
			Index:         i + 1,
			Timestamp:     g.timestamp,
			PromptSummary: g.promptSummary,
			Models:        models,
			Tokens:        tokens,
			CostUSD:       rawCost, // rescaled below once rawTotal is known
		})
	}

	if rawTotal > 0 && actualTotal > 0 {
		scale := actualTotal / rawTotal
		for i := range results {
			results[i].CostUSD *= scale
		}
	}

	return results, nil
}

func modelsLabel(models []string) string {
	if len(models) == 0 {
		return "-"
	}
	return strings.Join(models, ",")
}
