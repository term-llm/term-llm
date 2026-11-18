package clibin

import (
	"context"
	"fmt"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/mcphttp"
	"github.com/term-llm/term-llm/internal/runtimeoutput"
)

// prepareMCPBridge readies the tool bridge for a turn and writes the mcp.json
// that turn's cursor-agent process reads. It reports whether the bridge is
// exposed to the turn.
func (p *CursorBinProvider) prepareMCPBridge(ctx context.Context, home string, req core.Request) (bool, error) {
	exposeBridge := false
	if len(req.Tools) > 0 {
		if !p.toolExecutorConfigured {
			runtimeoutput.Warn("cursor-bin tools requested but no tool executor configured", "tool_count", len(req.Tools))
		} else if err := p.ensureMCPServer(ctx, req.Tools, req.Debug || req.DebugRaw); err != nil {
			return false, err
		} else {
			exposeBridge = true
		}
	}
	return exposeBridge, p.writeCursorMCPConfig(home, exposeBridge)
}

// ensureMCPServer keeps the running bridge while the tool surface is unchanged.
// The server's tool list is fixed when it starts, so a changed surface (an MCP
// server toggled between turns, for example) starts a replacement; the next
// mcp.json points cursor-agent at it. The old bridge is stopped only once the
// replacement is up, so a failed rebuild keeps the previous tool list.
func (p *CursorBinProvider) ensureMCPServer(ctx context.Context, tools []core.ToolSpec, debug bool) error {
	specs := mcpToolSpecs(tools)
	fingerprint, err := mcpToolSurfaceFingerprint(specs)
	if err != nil {
		if p.mcpServer != nil {
			runtimeoutput.Warn("cursor-bin cannot encode the MCP tool surface; keeping the current bridge", "error", err)
			return nil
		}
		return fmt.Errorf("encode cursor-bin MCP tool surface: %w", err)
	}
	if p.mcpServer != nil && fingerprint == p.mcpToolFingerprint {
		return nil
	}
	server := mcphttp.NewServer(p.cliToolBridgeState.wrappedExecutor(p.formatToolOutput))
	server.SetDebug(debug)
	url, token, err := server.Start(ctx, specs)
	if err != nil {
		if p.mcpServer != nil {
			runtimeoutput.Warn("cursor-bin MCP bridge rebuild failed; this turn keeps the previous tool list, which may include removed tools", "error", err)
			return nil
		}
		return fmt.Errorf("start cursor-bin MCP server: %w", err)
	}
	stopMCPBridge(p.mcpServer, "")
	p.mcpServer, p.mcpURL, p.mcpToken, p.mcpToolFingerprint = server, url, token, fingerprint
	return nil
}
