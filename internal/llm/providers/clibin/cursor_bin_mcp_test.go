package clibin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
)

func newCursorBridgeTestProvider(t *testing.T) (*CursorBinProvider, string) {
	t.Helper()
	home := t.TempDir()
	if err := ensureCursorHomeLayout(home); err != nil {
		t.Fatal(err)
	}
	p := NewCursorBinProvider("auto-smart", nil)
	// The bridge is only exposed when a tool executor is wired.
	p.SetToolExecutor(func(context.Context, string, json.RawMessage) (core.ToolOutput, error) {
		return core.TextOutput("ok"), nil
	})
	t.Cleanup(p.CleanupMCP)
	return p, home
}

// cursorTurnMCPConfig prepares the bridge for a turn with the given tools, as
// Stream does, and returns the mcp.json that turn's cursor-agent reads.
func cursorTurnMCPConfig(t *testing.T, p *CursorBinProvider, home string, tools []core.ToolSpec) string {
	t.Helper()
	exposed, err := p.prepareMCPBridge(context.Background(), home, core.Request{Tools: tools})
	if err != nil {
		t.Fatalf("prepare MCP bridge: %v", err)
	}
	if !exposed {
		t.Fatal("turn does not expose the MCP bridge")
	}
	return filepath.Join(home, "cwd", ".cursor", "mcp.json")
}

func TestCursorBinProviderRefreshesMCPBridgeWhenToolSurfaceChanges(t *testing.T) {
	p, home := newCursorBridgeTestProvider(t)
	schema := map[string]any{"type": "object"}
	initial := []core.ToolSpec{{Name: "alpha", Schema: schema}, {Name: "beta", Schema: schema}}

	config := cursorTurnMCPConfig(t, p, home, initial)
	if got := bridgeConfigToolNames(t, config); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("initial bridge tools = %v", got)
	}
	firstServer, firstURL, firstToken := p.mcpServer, p.mcpURL, p.mcpToken

	// The same surface in another order keeps the bridge, its URL and token.
	cursorTurnMCPConfig(t, p, home, []core.ToolSpec{initial[1], initial[0]})
	if p.mcpServer != firstServer || p.mcpURL != firstURL || p.mcpToken != firstToken {
		t.Fatal("unchanged tool surface rebuilt the bridge")
	}

	// A tool added between turns (an MCP server enabled in serve, for example)
	// must reach the next cursor-agent process.
	expanded := append(slices.Clone(initial), core.ToolSpec{Name: "gamma", Schema: schema})
	if got := bridgeConfigToolNames(t, cursorTurnMCPConfig(t, p, home, expanded)); !slices.Equal(got, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("bridge tools after expansion = %v, want gamma added", got)
	}
	if firstServer.URL() != "" {
		t.Fatal("previous bridge server is still running")
	}

	// Removing it again withdraws the tool.
	if got := bridgeConfigToolNames(t, cursorTurnMCPConfig(t, p, home, initial)); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("bridge tools after removal = %v, want gamma withdrawn", got)
	}

	// A changed description is a changed surface too.
	described := []core.ToolSpec{{Name: "alpha", Description: "now documented", Schema: schema}, initial[1]}
	if got := bridgeConfigTools(t, cursorTurnMCPConfig(t, p, home, described))["alpha"].Description; got != "now documented" {
		t.Fatalf("alpha description = %q, want the refreshed one", got)
	}
}

func TestCursorBinProviderKeepsMCPBridgeWhenToolSurfaceCannotBeEncoded(t *testing.T) {
	p, home := newCursorBridgeTestProvider(t)
	schema := map[string]any{"type": "object"}
	initial := []core.ToolSpec{{Name: "alpha", Schema: schema}}
	cursorTurnMCPConfig(t, p, home, initial)
	server := p.mcpServer

	// A schema that cannot be encoded leaves the working bridge in place
	// rather than failing the turn or dropping every tool.
	unencodable := map[string]any{"type": "object", "default": func() {}}
	broken := append(slices.Clone(initial), core.ToolSpec{Name: "broken", Schema: unencodable})
	config := cursorTurnMCPConfig(t, p, home, broken)
	if p.mcpServer != server {
		t.Fatal("unencodable tool surface replaced the bridge")
	}
	if got := bridgeConfigToolNames(t, config); !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("bridge tools = %v, want the previous bridge still serving", got)
	}
}
