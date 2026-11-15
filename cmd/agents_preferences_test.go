package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/term-llm/term-llm/internal/agents"
	"github.com/term-llm/term-llm/internal/config"
)

func TestAgentMCPPreferenceCommands(t *testing.T) {
	writeServeContractConfig(t, "default_provider: debug\nagents:\n  preferences:\n    developer:\n      model: test-model\n")
	for _, tc := range []struct {
		name  string
		value string
		get   string
		show  string
	}{
		{name: "replacement", value: "github,playwright", get: "  mcp: github, playwright", show: "MCP servers:\n  - github\n  - playwright"},
		{name: "disabled", value: "", get: "  mcp: []", show: "MCP servers: none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			captureStdout(t, func() {
				err = runAgentsPrefSet(agentsPrefSetCmd, []string{"developer", "mcp=" + tc.value})
			})
			if err != nil {
				t.Fatal(err)
			}
			get := captureStdout(t, func() {
				err = runAgentsPrefGet(agentsPrefGetCmd, []string{"developer"})
			})
			if err != nil || !strings.Contains(get, tc.get) {
				t.Fatalf("agents get = %q, %v; want %q", get, err, tc.get)
			}
			show := captureStdout(t, func() {
				err = runAgentsShow(agentsShowCmd, []string{"developer"})
			})
			if err != nil || !strings.Contains(show, tc.show) {
				t.Fatalf("agents show = %q, %v; want %q", show, err, tc.show)
			}
		})
	}
}

func TestAgentMCPPreferenceCompletion(t *testing.T) {
	writeServeContractConfig(t, "default_provider: debug\n")
	completions, _ := agentPrefSetCompletion(agentsPrefSetCmd, []string{"developer"}, "mc")
	if !reflect.DeepEqual(completions, []string{"mcp="}) {
		t.Fatalf("MCP preference completions = %v", completions)
	}
}

func TestAgentPreferenceClearCommand(t *testing.T) {
	writeServeContractConfig(t, "default_provider: debug\nagents:\n  preferences:\n    developer:\n      model: preferred\n      mcp: []\n")
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "term-llm", "agents", "developer")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("name: developer\nmcp: [{name: baseline}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	output := captureStdout(t, func() {
		err = agentsPrefClearCmd.RunE(agentsPrefClearCmd, []string{"developer", "mcp"})
	})
	if err != nil || !strings.Contains(output, "Cleared preferences for 'developer': mcp") {
		t.Fatalf("agents clear = %q, %v", output, err)
	}
	pref, found := config.GetAgentPreference("developer")
	if !found || pref.MCP != nil || pref.Model != "preferred" {
		t.Fatalf("clearing MCP changed other preferences: %+v (found %v)", pref, found)
	}
	output = captureStdout(t, func() {
		err = runAgentsShow(agentsShowCmd, []string{"developer"})
	})
	if err != nil || !strings.Contains(output, "MCP servers:\n  - baseline") {
		t.Fatalf("clearing did not restore the definition's MCP defaults: %q, %v", output, err)
	}
}

func TestAgentPreferenceClearArgsAndCompletion(t *testing.T) {
	writeServeContractConfig(t, "default_provider: debug\n")
	if err := agentsPrefClearCmd.Args(agentsPrefClearCmd, nil); err == nil {
		t.Fatal("clear command must require an agent")
	}
	for _, args := range [][]string{{"developer"}, {"developer", "mcp"}, {"developer", "model", "mcp"}} {
		if err := agentsPrefClearCmd.Args(agentsPrefClearCmd, args); err != nil {
			t.Fatalf("clear command rejected %v: %v", args, err)
		}
	}
	keys, directive := agentPrefClearCompletion(agentsPrefClearCmd, []string{"developer"}, "mc")
	if !reflect.DeepEqual(keys, []string{"mcp"}) || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("clear key completion = %v, %v", keys, directive)
	}
	keys, _ = agentPrefClearCompletion(agentsPrefClearCmd, []string{"developer", "mcp"}, "mc")
	if len(keys) != 0 {
		t.Fatalf("already selected key was suggested: %v", keys)
	}
	if err := runAgentsPrefClear(agentsPrefClearCmd, []string{"developer", "typo"}); err == nil || !strings.Contains(err.Error(), "unknown agent preference key") {
		t.Fatalf("unknown clear key error = %v", err)
	}
}

func TestShowAgentMCPEmptySelection(t *testing.T) {
	output := captureStdout(t, func() {
		showAgentMCP(&agents.Agent{MCP: []agents.MCPConfig{}})
	})
	if output != "\nMCP servers: none\n" {
		t.Fatalf("empty MCP section = %q, want a blank separator before the heading", output)
	}
}
