package clibin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/llm/core"
)

func claudeAssistantUsageLine(id, parent string, input, cacheRead, cacheWrite int) string {
	parentField := `null`
	if parent != "" {
		parentField = `"` + parent + `"`
	}
	return `{"type":"assistant","parent_tool_use_id":` + parentField + `,"message":{"id":"` + id +
		`","model":"claude-opus-5-5","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":` + strconv.Itoa(input) +
		`,"cache_read_input_tokens":` + strconv.Itoa(cacheRead) + `,"cache_creation_input_tokens":` + strconv.Itoa(cacheWrite) +
		`,"output_tokens":16}}}`
}

// Shapes mirror a real Claude Code 2.1.303 stream-json tool loop: per-call
// usage on each assistant event, and a result whose usage is summed across all
// calls while modelUsage carries the context window.
func TestClaudeBinReportsContextPerAPICall(t *testing.T) {
	p := NewClaudeBinProvider("", nil)
	events := make(chan core.Event, 32)
	send := eventSender{ctx: context.Background(), ch: events}
	var (
		lastUsage       *core.Usage
		sawTextDelta    bool
		fallbackText    string
		handledTerminal bool
	)
	lines := []string{
		claudeAssistantUsageLine("msg_1", "", 2, 12597, 12915),
		// Same API call repeated for a second content block: no new reading.
		claudeAssistantUsageLine("msg_1", "", 2, 12597, 12915),
		// A subagent call runs in its own window and must not move the meter.
		claudeAssistantUsageLine("msg_side", "toolu_1", 2, 900000, 0),
		claudeAssistantUsageLine("msg_2", "", 2, 25512, 78),
		claudeAssistantUsageLine("msg_3", "", 2, 25590, 78),
		`{"type":"result","subtype":"success","is_error":false,"result":"done","usage":{"input_tokens":6,"cache_creation_input_tokens":13071,"cache_read_input_tokens":63699,"output_tokens":184},"modelUsage":{"claude-opus-5-5":{"inputTokens":6,"contextWindow":1000000,"maxOutputTokens":128000}}}`,
	}
	for _, line := range lines {
		if err := p.handleClaudeLine(context.Background(), line, false, send, &lastUsage, &sawTextDelta, &fallbackText, &handledTerminal, nil, false); err != nil {
			t.Fatalf("handleClaudeLine: %v", err)
		}
	}
	close(events)

	var got []core.ContextUsage
	for ev := range events {
		if ev.Type == core.EventContextUsage {
			got = append(got, *ev.Context)
		}
	}
	want := []core.ContextUsage{
		{UsedTokens: 25514},
		{UsedTokens: 25592},
		{UsedTokens: 25670},
		{UsedTokens: 25670, Window: 1000000},
	}
	if len(got) != len(want) {
		t.Fatalf("context readings = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("context reading %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The window is known from the first turn onwards, so later turns report
	// it with every mid-turn checkpoint.
	events = make(chan core.Event, 4)
	send = eventSender{ctx: context.Background(), ch: events}
	if err := p.handleClaudeLine(context.Background(), claudeAssistantUsageLine("msg_4", "", 3, 25700, 200), false, send, &lastUsage, &sawTextDelta, &fallbackText, &handledTerminal, nil, false); err != nil {
		t.Fatalf("handleClaudeLine: %v", err)
	}
	close(events)
	ev := <-events
	if ev.Type != core.EventContextUsage || *ev.Context != (core.ContextUsage{UsedTokens: 25903, Window: 1000000}) {
		t.Fatalf("next turn reading = %+v, want 25903/1000000", ev.Context)
	}

	// A mid-run model change (e.g. Claude Code's fallback model) invalidates
	// the window until that model's result reports its own.
	events = make(chan core.Event, 4)
	send = eventSender{ctx: context.Background(), ch: events}
	sonnet := strings.Replace(claudeAssistantUsageLine("msg_5", "", 3, 26000, 0), "claude-opus-5-5", "claude-sonnet-5", 1)
	if err := p.handleClaudeLine(context.Background(), sonnet, false, send, &lastUsage, &sawTextDelta, &fallbackText, &handledTerminal, nil, false); err != nil {
		t.Fatalf("handleClaudeLine: %v", err)
	}
	close(events)
	ev = <-events
	if ev.Type != core.EventContextUsage || *ev.Context != (core.ContextUsage{UsedTokens: 26003}) {
		t.Fatalf("model-change reading = %+v, want 26003 with unknown window", ev.Context)
	}
}

func TestClaudeBinEphemeralRequestsDoNotReportContext(t *testing.T) {
	p := NewClaudeBinProvider("", nil)
	events := make(chan core.Event, 4)
	send := eventSender{ctx: context.Background(), ch: events}
	var (
		lastUsage       *core.Usage
		sawTextDelta    bool
		fallbackText    string
		handledTerminal bool
	)
	if err := p.handleClaudeLine(context.Background(), claudeAssistantUsageLine("msg_1", "", 2, 100, 100), false, send, &lastUsage, &sawTextDelta, &fallbackText, &handledTerminal, nil, true); err != nil {
		t.Fatalf("handleClaudeLine: %v", err)
	}
	close(events)
	for ev := range events {
		if ev.Type == core.EventContextUsage {
			t.Fatalf("ephemeral request reported context %+v", ev.Context)
		}
	}
}

func TestClaudeResultWindowMatchesAssistantModel(t *testing.T) {
	const model = "claude-sonnet-4-6"
	for _, tt := range []struct {
		name, usage string
		want        int
	}{
		{"exact before alias", `"claude-sonnet-4-6":{"contextWindow":200000},"sonnet[1m]":{"canonicalModel":"claude-sonnet-4-6","contextWindow":1000000}`, 200000},
		{"extended selector", `"claude-sonnet-4-6[1m]":{"contextWindow":1000000},"haiku":{"contextWindow":200000}`, 1000000},
		{"reported alias", `"sonnet[1m]":{"canonicalModel":"claude-sonnet-4-6","contextWindow":500000},"haiku":{"contextWindow":200000}`, 500000},
		{"ambiguous aliases", `"sonnet":{"canonicalModel":"claude-sonnet-4-6","contextWindow":200000},"sonnet[1m]":{"canonicalModel":"claude-sonnet-4-6","contextWindow":1000000}`, 0},
		{"unrelated singleton", `"haiku":{"contextWindow":200000}`, 0},
		{"unresolved alias", `"sonnet[1m]":{"contextWindow":1000000}`, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := claudeContextReport{model: model, used: 123}
			got := r.observe("result", `{"type":"result","modelUsage":{`+tt.usage+`}}`)
			if tt.want == 0 {
				if got != nil {
					t.Fatalf("unexpected reading %+v", got)
				}
				return
			}
			if got == nil || *got != (core.ContextUsage{UsedTokens: 123, Window: tt.want}) {
				t.Fatalf("reading = %+v, want 123/%d", got, tt.want)
			}
		})
	}
}

// Only display fields from a real get_context_usage summary are needed.
const claudeSummaryResponse = `{"type":"control_response","response":{"subtype":"success","request_id":"probe","response":{"model":"claude-sonnet-5-5","maxTokens":1000000,"rawMaxTokens":1000000,"autoCompactThreshold":967000,"isAutoCompactEnabled":true}}}`

func TestClaudeProbeValidation(t *testing.T) {
	for _, tt := range []struct {
		name, from, to string
		window, want   int
	}{
		{"matching", "", "", 1000000, 967000},
		{"before result", "", "", 0, 967000},
		{"other request", `"probe"`, `"interrupt"`, 0, 0},
		{"error", `"success"`, `"error"`, 0, 0},
		{"unknown model", `"claude-sonnet-5-5"`, `""`, 0, 0},
		{"other model", `"claude-sonnet-5-5"`, `"haiku"`, 0, 0},
		{"window mismatch", "", "", 200000, 0},
		{"unknown window", `"maxTokens":1000000,"rawMaxTokens":1000000,`, ``, 0, 0},
		{"maxTokens fallback", `"rawMaxTokens":1000000,`, ``, 0, 967000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := claudeContextReport{model: "claude-sonnet-5-5", used: 100, requestID: "probe", window: tt.window}
			got := r.observe("control_response", strings.Replace(claudeSummaryResponse, tt.from, tt.to, 1))
			if tt.want == 0 {
				if got != nil {
					t.Fatalf("unexpected reading %+v", got)
				}
				return
			}
			if got == nil || *got != (core.ContextUsage{UsedTokens: 100, Window: 1000000, ProviderAutoCompactAt: tt.want}) {
				t.Fatalf("reading = %+v", got)
			}
		})
	}
}

func TestClaudeProbeWindowWithoutThreshold(t *testing.T) {
	assistant := func(id string) string {
		return strings.Replace(claudeAssistantUsageLine(id, "", 100, 0, 0), "claude-opus-5-5", "claude-sonnet-5-5", 1)
	}
	want := core.ContextUsage{UsedTokens: 100, Window: 1000000}
	for _, tt := range []struct{ name, from, to string }{
		{"disabled", `true`, `false`},
		{"unknown enabled", `"isAutoCompactEnabled"`, `"other"`},
		{"unknown threshold", `"autoCompactThreshold"`, `"other"`},
		{"above effective window", `"maxTokens":1000000`, `"maxTokens":200000`},
		{"above window", `967000`, `1000001`},
		{"negative", `967000`, `-1`},
		{"wrong threshold type", `967000`, `"967000"`},
	} {
		for _, order := range []string{"summary first", "assistant first"} {
			t.Run(tt.name+"/"+order, func(t *testing.T) {
				r := claudeContextReport{requestID: "probe"}
				if order == "assistant first" {
					r.observe("assistant", assistant("one"))
				}
				got := r.observe("control_response", strings.Replace(claudeSummaryResponse, tt.from, tt.to, 1))
				if order == "summary first" {
					if got != nil {
						t.Fatalf("published before assistant usage: %+v", got)
					}
					got = r.observe("assistant", assistant("one"))
				}
				if got == nil || *got != want {
					t.Fatalf("reading = %+v, want %+v", got, want)
				}
				r.observe("result", `{"type":"result"}`)
				if got := r.observe("assistant", assistant("two")); got == nil || *got != want {
					t.Fatalf("reading after result without modelUsage = %+v, want %+v", got, want)
				}
			})
		}
	}
}

func TestClaudeProbeDoesNotSurviveFallback(t *testing.T) {
	r := claudeContextReport{requestID: "probe"}
	line := strings.ReplaceAll(claudeAssistantUsageLine("one", "", 100, 0, 0), "claude-opus-5-5", "claude-sonnet-5-5")
	if got := r.observe("control_response", claudeSummaryResponse); got != nil {
		t.Fatal("published before assistant usage")
	}
	if got := r.observe("assistant", line); got.ProviderAutoCompactAt != 967000 {
		t.Fatalf("reading = %+v", got)
	}
	fallback := claudeAssistantUsageLine("two", "", 120, 0, 0)
	for _, line := range []string{fallback, line} {
		if got := r.observe("assistant", line); got.ProviderAutoCompactAt != 0 {
			t.Fatalf("stale threshold %+v", got)
		}
	}
}

// cutoffInput accepts exactly limit bytes and fails the first write crossing it.
type cutoffInput struct {
	accepted []byte
	limit    int
	failed   bool
}

func (w *cutoffInput) Write(p []byte) (int, error) {
	n := min(len(p), w.limit-len(w.accepted))
	w.accepted = append(w.accepted, p[:n]...)
	if n < len(p) {
		w.failed = true
		return n, syscall.EPIPE
	}
	return n, nil
}

func TestClaudeStartupWriteBrokenPipe(t *testing.T) {
	const prompt = `{"type":"user","message":{"role":"user","content":"hello"}}`
	for _, tc := range []struct {
		name  string
		limit int
		fatal bool
	}{
		{"before user newline", len(prompt), true},
		{"after user newline", len(prompt) + 1, false},
		{"within optional probe", len(prompt) + 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &cutoffInput{limit: tc.limit}
			var r claudeContextReport
			err := r.writeInitialInput(w, prompt, false)
			if !w.failed || string(w.accepted) != (prompt + "\n" + `{"type"`)[:tc.limit] {
				t.Fatal("writer did not break at the expected frame boundary")
			}
			if tc.fatal && !errors.Is(err, syscall.EPIPE) || !tc.fatal && err != nil {
				t.Fatalf("startup write after %d accepted bytes: err=%v, want fatal=%t", tc.limit, err, tc.fatal)
			}
		})
	}
}

func TestClaudeContextProbeSubprocess(t *testing.T) {
	const script = `#!/usr/bin/env python3
import json, sys
user = json.loads(sys.stdin.readline())
assert user['type'] == 'user'
if user['text'] != 'ephemeral':
    req = json.loads(sys.stdin.readline())
    assert req['type'] == 'control_request' and req['request'] == {'subtype':'get_context_usage','detail':'summary'}
    if user['text'] == 'reply':
        resp = json.loads('` + claudeSummaryResponse + `')
        resp['response']['request_id'] = req['request_id']
        resp['response']['response']['memoryFiles'] = [{'path':'PRIVATE-SENTINEL'}]
        print(json.dumps(resp), flush=True)
print('{"type":"assistant","message":{"id":"one","model":"claude-sonnet-5-5","usage":{"input_tokens":123}}}', flush=True)
print('{"type":"result","result":"done"}', flush=True)
assert not sys.stdin.read()
if user['text'] == 'reply': sys.exit(42)
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := NewClaudeBinProvider("sonnet", nil)
	for _, mode := range []string{"reply", "ignore", "ephemeral"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			events := make(chan core.Event, 16)
			err := p.runClaudeCommand(ctx, nil, "", `{"type":"user","text":"`+mode+`"}`, "", false, eventSender{ctx: ctx, ch: events}, mode == "ephemeral", false)
			if mode == "reply" {
				var ce *CLICommandError
				if !errors.As(err, &ce) || ce.ExitCode != 42 || strings.Contains(ce.StdoutTail, "PRIVATE-SENTINEL") {
					t.Fatal("expected a sanitized CLI failure")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			close(events)
			found := false
			for ev := range events {
				if ev.Type != core.EventContextUsage {
					continue
				}
				found = true
				want := 0
				if mode == "reply" {
					want = 967000
				}
				if ev.Context.ProviderAutoCompactAt != want {
					t.Fatalf("unexpected reading %+v", ev.Context)
				}
			}
			if found != (mode != "ephemeral") {
				t.Fatalf("context reported = %t", found)
			}
		})
	}
}
