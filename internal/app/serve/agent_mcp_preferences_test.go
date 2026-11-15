package serve

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/mockprovider"
	"github.com/term-llm/term-llm/internal/mcp"
	runpkg "github.com/term-llm/term-llm/internal/run"
)

func TestRunnerAgentMCPPreferences(t *testing.T) {
	for _, tc := range []struct {
		name       string
		preference string
		explicit   string
		restored   *SessionSettings
		child      bool
		platform   string
		denied     bool
		want       string
	}{
		{name: "definition", preference: "{}", want: "original"},
		{name: "replacement", preference: "{mcp: [selected]}", want: "selected"},
		{name: "disabled", preference: "{mcp: []}"},
		{name: "explicit", preference: "{mcp: [selected]}", explicit: "original", want: "original"},
		{name: "explicit over disabled", preference: "{mcp: []}", explicit: "original", want: "original"},
		{name: "restored", preference: "{mcp: [selected]}", restored: &SessionSettings{MCP: "original"}, want: "original"},
		{name: "restored disabled", preference: "{mcp: [selected]}", restored: &SessionSettings{}},
		{name: "child", preference: "{mcp: [selected]}", child: true, want: "selected"},
		{name: "job", preference: "{mcp: [selected]}", platform: runpkg.PlatformJob, want: "selected"},
		{name: "policy denied", preference: "{mcp: [selected]}", denied: true},
		{name: "missing", preference: "{mcp: [removed]}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := mcp.ServerConfig{Command: os.Args[0], Env: map[string]string{runServeMCPHandlerTestServerEnv: "1"}}
			writeServeMCPConfig(t, map[string]mcp.ServerConfig{"original": server, "selected": server})
			body := "default_provider: debug\nagents:\n  preferences:\n    custom: " + tc.preference + "\n"
			if tc.denied {
				body += "mcp: {servers: {selected: {agents: []}}}\n"
			}
			writeServePolicy(t, body)
			dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "term-llm", "agents", "custom")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("name: custom\nmcp: [{name: original}]\n"), 0600); err != nil {
				t.Fatal(err)
			}
			loaded, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{
				DefaultProvider: "mock",
				Providers:       map[string]config.ProviderConfig{"mock": {Model: "mock-model"}},
				Agents:          loaded.Agents,
			}
			var warnings bytes.Buffer
			runner := newCmdRunner(cfg, cmdRunnerOptions{ErrWriter: &warnings, RestoreSettings: tc.restored}).(*cmdRunner)
			platform := tc.platform
			if platform == "" {
				platform = runpkg.PlatformConsole
			}
			env, err := runner.prepare(context.Background(), runpkg.Request{
				AgentName:        "custom",
				Platform:         platform,
				IsSubagent:       tc.child,
				MCP:              tc.explicit,
				MCPExplicit:      tc.explicit != "",
				ProviderInstance: mockprovider.NewMockProvider("mock"),
				Cwd:              t.TempDir(),
				DeferSession:     true,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer env.Close()
			if env.runtime.mcpSetting != tc.want {
				t.Fatalf("MCP selection = %q, want %q", env.runtime.mcpSetting, tc.want)
			}
			if tc.want == "" {
				if env.runtime.mcpManager != nil && len(env.runtime.mcpManager.EnabledServers()) != 0 {
					t.Fatal("empty selection started a server")
				}
			} else if env.runtime.mcpManager == nil || strings.Join(env.runtime.mcpManager.EnabledServers(), ",") != tc.want {
				t.Fatalf("selected server %q was not started", tc.want)
			}
			if (tc.denied || tc.name == "missing") && !strings.Contains(warnings.String(), "dropping MCP server") {
				t.Fatalf("missing repair warning: %s", warnings.String())
			}
		})
	}
}
