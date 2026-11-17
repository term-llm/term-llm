package clibin

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/term-llm/term-llm/internal/llm/core"
)

// claudeContextReport tracks the context occupancy Claude Code reports while
// it runs its own tool loop. It is display-only: Claude Code owns compaction
// and term-llm never acts on these numbers.
//
// Each top-level `assistant` stream event carries the usage of the API call
// that produced it, so its prompt size (input + cache read + cache write) is
// the live context size at every tool-call checkpoint. The terminal `result`
// usage is summed across every call in the turn and is unusable for this;
// only its modelUsage[*].contextWindow is taken from it.
type claudeContextReport struct {
	model     string // Model of the latest top-level assistant message
	messageID string // Assistant message the last reading came from
	used      int    // Prompt tokens of the latest top-level API call
	window    int    // Context window Claude Code reported for model
	requestID string // Outstanding summary request in this subprocess only
	summary   claudeContextSummary
}

// Parse only the display fields. Even a summary can include private paths,
// tool names and other breakdowns; never put the response in diagnostics.
type claudeContextSummary struct {
	Model             string
	Window, Threshold int
}

type claudeSummaryResponseFields struct {
	Model     string          `json:"model"`
	Window    int             `json:"rawMaxTokens"`
	MaxTokens int             `json:"maxTokens"`
	Enabled   json.RawMessage `json:"isAutoCompactEnabled"`
	Threshold json.RawMessage `json:"autoCompactThreshold"`
}

// Malformed optional compaction metadata does not invalidate the window.
func (s claudeSummaryResponseFields) validated() claudeContextSummary {
	window := s.Window
	if window == 0 {
		window = s.MaxTokens
	}
	if window <= 0 || s.Model == "" {
		return claudeContextSummary{}
	}
	summary := claudeContextSummary{Model: s.Model, Window: window}
	var enabled bool
	var threshold int
	if json.Unmarshal(s.Enabled, &enabled) == nil && enabled && json.Unmarshal(s.Threshold, &threshold) == nil && threshold > 0 && threshold <= window && (s.MaxTokens == 0 || threshold <= s.MaxTokens) {
		summary.Threshold = threshold
	}
	return summary
}

// writeInitialInput sends the user frame before the optional probe, while stdin
// is still private to startup. A CLI may close stdin once it accepts the turn.
func (r *claudeContextReport) writeInitialInput(stdin io.Writer, userPrompt string, ephemeral bool) error {
	if _, err := io.WriteString(stdin, userPrompt+"\n"); err != nil || ephemeral {
		return err
	}
	// Keep only the previously learned result window for the same model.
	id := uuid.NewString()
	*r = claudeContextReport{model: r.model, window: r.window, requestID: id}
	// Full detail may make token-count API calls. Summary uses local estimates.
	if _, err := io.WriteString(stdin, `{"type":"control_request","request_id":"`+id+`","request":{"subtype":"get_context_usage","detail":"summary"}}`+"\n"); err != nil {
		r.requestID = ""
	}
	return nil
}

func (r *claudeContextReport) observeSummary(line string) *core.ContextUsage {
	if r.requestID == "" {
		return nil
	}
	var msg struct {
		Response struct {
			RequestID string                      `json:"request_id"`
			Subtype   string                      `json:"subtype"`
			Summary   claudeSummaryResponseFields `json:"response"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(line), &msg) != nil || msg.Response.RequestID != r.requestID {
		return nil
	}
	r.requestID = ""
	if msg.Response.Subtype != "success" {
		return nil
	}
	s := msg.Response.Summary.validated()
	if s.Window == 0 {
		return nil
	}
	r.summary = s
	if r.used == 0 || !r.matchesSummary() {
		return nil
	}
	return r.reading()
}

type claudeAssistantUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type claudeAssistantContextLine struct {
	ParentToolUseID string `json:"parent_tool_use_id"`
	Message         struct {
		ID    string                `json:"id"`
		Model string                `json:"model"`
		Usage *claudeAssistantUsage `json:"usage"`
	} `json:"message"`
}

type claudeModelUsage struct {
	ContextWindow  int    `json:"contextWindow"`
	CanonicalModel string `json:"canonicalModel"`
}

type claudeResultContextLine struct {
	ModelUsage map[string]claudeModelUsage `json:"modelUsage"`
}

// observe updates the report from one stream-json line and returns the
// reading to publish, or nil when the line carries nothing new.
func (r *claudeContextReport) observe(lineType, line string) *core.ContextUsage {
	switch lineType {
	case "control_response":
		return r.observeSummary(line)
	case "assistant":
		return r.observeAssistant(line)
	case "result":
		return r.observeResult(line)
	}
	return nil
}

func (r *claudeContextReport) observeAssistant(line string) *core.ContextUsage {
	var msg claudeAssistantContextLine
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return nil
	}
	// Subagent (sidechain) calls run in their own context window.
	if msg.ParentToolUseID != "" {
		return nil
	}
	if msg.Message.Model != r.model {
		if r.used != 0 { // A fallback mid-turn invalidates even an in-flight probe.
			r.requestID, r.summary = "", claudeContextSummary{}
		}
		r.model, r.window, r.messageID, r.used = msg.Message.Model, 0, "", 0
	}
	if msg.Message.Usage == nil {
		return nil
	}
	u := msg.Message.Usage
	used := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	if used <= 0 {
		return nil
	}
	// Claude Code repeats the message usage on every content block event of
	// the same API call.
	if msg.Message.ID != "" && msg.Message.ID == r.messageID && used == r.used {
		return nil
	}
	r.messageID, r.used = msg.Message.ID, used
	return r.reading()
}

func (r *claudeContextReport) observeResult(line string) *core.ContextUsage {
	var msg claudeResultContextLine
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return nil
	}
	window := msg.windowFor(r.model)
	if window <= 0 || window == r.window {
		return nil
	}
	r.window = window
	if r.used <= 0 {
		return nil
	}
	return r.reading()
}

// Claude Code keys modelUsage by raw model ID. An alias or [1m] selector
// may differ from the model in the assistant message. Only accept an
// indirect match when it identifies one entry; side calls have windows too.
func (msg claudeResultContextLine) windowFor(model string) int {
	if model == "" {
		return 0
	}
	if usage, ok := msg.ModelUsage[model]; ok {
		return usage.ContextWindow
	}
	base := strings.TrimSuffix(model, "[1m]")
	window, found := 0, false
	for key, usage := range msg.ModelUsage {
		if strings.TrimSuffix(key, "[1m]") != base && usage.CanonicalModel != base {
			continue
		}
		if found {
			return 0
		}
		window, found = usage.ContextWindow, true
	}
	return window
}

func (r *claudeContextReport) matchesSummary() bool {
	s := r.summary
	return s.Model != "" && strings.TrimSuffix(s.Model, "[1m]") == strings.TrimSuffix(r.model, "[1m]") && (r.window == 0 || r.window == s.Window)
}

func (r *claudeContextReport) reading() *core.ContextUsage {
	reading := &core.ContextUsage{UsedTokens: r.used, Window: r.window}
	if r.matchesSummary() {
		s := r.summary
		reading.Window, reading.ProviderAutoCompactAt = s.Window, s.Threshold
	}
	return reading
}

func claudeStreamLineType(line string) string {
	var message struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(line), &message) != nil {
		return ""
	}
	return message.Type
}

// reportClaudeContext publishes a context reading derived from line. Ephemeral
// one-shot requests run outside the conversation and never report.
func (p *ClaudeBinProvider) reportClaudeContext(lineType, line string, ephemeral bool, send eventSender) error {
	if ephemeral {
		return nil
	}
	reading := p.contextReport.observe(lineType, line)
	if reading == nil {
		return nil
	}
	return send.Send(core.Event{Type: core.EventContextUsage, Context: reading})
}
