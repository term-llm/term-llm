package cmd

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/term-llm/term-llm/internal/agents"
	appagent "github.com/term-llm/term-llm/internal/app/agent"
	"github.com/term-llm/term-llm/internal/config"
)

func runAgentsPrefGet(cmd *cobra.Command, args []string) error {
	agentName := args[0]

	pref, found := config.GetAgentPreference(agentName)
	if !found {
		fmt.Printf("No preferences set for agent '%s'\n", agentName)
		return nil
	}

	appagent.WritePreferences(os.Stdout, agentName, pref)

	return nil
}

func runAgentsPrefClear(cmd *cobra.Command, args []string) error {
	return appagent.ClearPreferences(os.Stdout, args[0], args[1:]...)
}

func agentPrefClearCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return agentNameCompletion(cmd, args, toComplete)
	}
	var keys []string
	for key := range config.KnownAgentPreferenceKeys {
		if strings.HasPrefix(key, toComplete) && !slices.Contains(args[1:], key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys, cobra.ShellCompDirectiveNoFileComp
}

func showAgentMCP(agent *agents.Agent) {
	if agent.MCP == nil {
		return
	}
	fmt.Println()
	if len(agent.MCP) == 0 {
		fmt.Println("MCP servers: none")
		return
	}
	fmt.Println("MCP servers:")
	for _, m := range agent.MCP {
		if m.Command != "" {
			fmt.Printf("  - %s: %s\n", m.Name, m.Command)
		} else {
			fmt.Printf("  - %s\n", m.Name)
		}
	}
}
