package agent

import (
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestWritePreferencesMCP(t *testing.T) {
	empty := []string{}
	names := []string{"github", "playwright"}
	for _, tc := range []struct {
		name string
		mcp  *[]string
		want string
	}{
		{name: "inherit"},
		{name: "disabled", mcp: &empty, want: "  mcp: []\n"},
		{name: "replacement", mcp: &names, want: "  mcp: github, playwright\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			WritePreferences(&out, "developer", config.AgentPreference{MCP: tc.mcp})
			if want := "Preferences for 'developer':\n" + tc.want; out.String() != want {
				t.Fatalf("preferences = %q, want %q", out.String(), want)
			}
		})
	}
}

func TestWritePreferencesExistingFields(t *testing.T) {
	no := false
	turns, parallel, depth, timeout := 50, 3, 2, 60
	pref := config.AgentPreference{
		Provider: "debug", Model: "fast",
		ToolsEnabled: []string{"read_file"}, ToolsDisabled: []string{"shell"},
		ShellAllow: []string{"git *"}, ShellAutoRun: &no,
		SpawnMaxParallel: &parallel, SpawnMaxDepth: &depth, SpawnTimeout: &timeout,
		SpawnAllowedAgents: []string{"codebase"}, AllowedModels: []string{"debug:*"},
		MaxTurns: &turns, Search: &no,
	}
	var out strings.Builder
	WritePreferences(&out, "developer", pref)
	for _, line := range []string{
		"provider: debug", "model: fast", "tools_enabled: read_file", "tools_disabled: shell",
		"shell_allow: git *", "shell_auto_run: false", "spawn_max_parallel: 3",
		"spawn_max_depth: 2", "spawn_timeout: 60", "spawn_allowed_agents: codebase",
		"allowed_models: debug:*", "max_turns: 50", "search: false",
	} {
		if !strings.Contains(out.String(), "  "+line+"\n") {
			t.Errorf("missing %q in preferences: %s", line, out.String())
		}
	}
}
