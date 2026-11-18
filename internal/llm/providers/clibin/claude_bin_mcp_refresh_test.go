package clibin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
)

// claudeTurnMCPConfig returns the --mcp-config path buildArgs hands to the
// Claude Code process for a turn with the given tools.
func claudeTurnMCPConfig(t *testing.T, p *ClaudeBinProvider, tools []core.ToolSpec) string {
	t.Helper()
	args, _ := p.buildArgs(context.Background(), core.Request{Tools: tools}, eventSender{})
	for i, arg := range args {
		if arg == "--mcp-config" && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("turn args have no --mcp-config: %q", args)
	return ""
}

func newClaudeBridgeTestProvider(t *testing.T) *ClaudeBinProvider {
	t.Helper()
	p := NewClaudeBinProvider("sonnet", nil)
	// buildArgs only exposes the bridge when a tool executor is wired.
	p.SetToolExecutor(func(context.Context, string, json.RawMessage) (core.ToolOutput, error) {
		return core.TextOutput("ok"), nil
	})
	t.Cleanup(p.CleanupMCP)
	return p
}

func TestClaudeBinProviderRefreshesMCPBridgeWhenToolSurfaceChanges(t *testing.T) {
	p := newClaudeBridgeTestProvider(t)
	schema := map[string]any{"type": "object"}
	initial := []core.ToolSpec{{Name: "alpha", Schema: schema}, {Name: "beta", Schema: schema}}

	first := claudeTurnMCPConfig(t, p, initial)
	if got := bridgeConfigToolNames(t, first); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("initial bridge tools = %v", got)
	}
	firstServer := p.mcpServer

	// The same surface in another order keeps the bridge, its URL and token.
	if got := claudeTurnMCPConfig(t, p, []core.ToolSpec{initial[1], initial[0]}); got != first || p.mcpServer != firstServer {
		t.Fatalf("unchanged tool surface rebuilt the bridge: %q -> %q", first, got)
	}

	// A tool added between turns (an MCP server enabled in serve, for example)
	// must reach the next Claude Code process.
	expanded := append(slices.Clone(initial), core.ToolSpec{Name: "gamma", Schema: schema})
	second := claudeTurnMCPConfig(t, p, expanded)
	if got := bridgeConfigToolNames(t, second); !slices.Equal(got, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("bridge tools after expansion = %v, want gamma added", got)
	}
	if firstServer.URL() != "" {
		t.Fatal("previous bridge server is still running")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("previous MCP config %q left behind: %v", first, err)
	}

	// Removing it again withdraws the tool.
	third := claudeTurnMCPConfig(t, p, initial)
	if got := bridgeConfigToolNames(t, third); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("bridge tools after removal = %v, want gamma withdrawn", got)
	}

	// A changed description is a changed surface too.
	described := []core.ToolSpec{{Name: "alpha", Description: "now documented", Schema: schema}, initial[1]}
	fourth := claudeTurnMCPConfig(t, p, described)
	if got := bridgeConfigTools(t, fourth)["alpha"].Description; got != "now documented" {
		t.Fatalf("alpha description = %q, want the refreshed one", got)
	}
}

func TestClaudeBinProviderKeepsMCPBridgeWhenRebuildFails(t *testing.T) {
	p := newClaudeBridgeTestProvider(t)
	schema := map[string]any{"type": "object"}
	initial := []core.ToolSpec{{Name: "alpha", Schema: schema}}
	first := claudeTurnMCPConfig(t, p, initial)
	expanded := append(slices.Clone(initial), core.ToolSpec{Name: "gamma", Schema: schema})

	// The replacement config file cannot be written (os.CreateTemp honours
	// TMPDIR on Unix): the turn must keep the working bridge rather than lose
	// every tool.
	tmp := os.Getenv("TMPDIR")
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if got := claudeTurnMCPConfig(t, p, expanded); got != first {
		t.Fatalf("failed rebuild returned %q, want the previous config %q", got, first)
	}
	if got := bridgeConfigToolNames(t, first); !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("previous bridge tools = %v, want it still serving", got)
	}

	// The change is retried on the next turn.
	t.Setenv("TMPDIR", tmp)
	if got := bridgeConfigToolNames(t, claudeTurnMCPConfig(t, p, expanded)); !slices.Equal(got, []string{"alpha", "gamma"}) {
		t.Fatalf("bridge tools after retry = %v, want gamma added", got)
	}
}
