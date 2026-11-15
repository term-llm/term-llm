package agent

import (
	"fmt"
	"io"
	"strings"

	"github.com/term-llm/term-llm/internal/config"
)

// ClearPreferences clears selected user overrides, or all overrides when keys
// is empty, and reports the scope without changing the remaining preferences.
func ClearPreferences(w io.Writer, agentName string, keys ...string) error {
	if err := config.ClearAgentPreferences(agentName, keys...); err != nil {
		return fmt.Errorf("clear preferences: %w", err)
	}
	if len(keys) == 0 {
		fmt.Fprintf(w, "Cleared preferences for '%s'\n", agentName)
	} else {
		fmt.Fprintf(w, "Cleared preferences for '%s': %s\n", agentName, strings.Join(keys, ", "))
	}
	return nil
}

// WritePreferences displays the user overrides for an agent, including an
// explicit empty MCP list so disabling defaults is distinct from inheritance.
func WritePreferences(w io.Writer, agentName string, pref config.AgentPreference) {
	fmt.Fprintf(w, "Preferences for '%s':\n", agentName)

	// Model preferences
	if pref.Provider != "" {
		fmt.Fprintf(w, "  provider: %s\n", pref.Provider)
	}
	if pref.Model != "" {
		fmt.Fprintf(w, "  model: %s\n", pref.Model)
	}

	// Tool configuration
	if len(pref.ToolsEnabled) > 0 {
		fmt.Fprintf(w, "  tools_enabled: %s\n", strings.Join(pref.ToolsEnabled, ", "))
	}
	if len(pref.ToolsDisabled) > 0 {
		fmt.Fprintf(w, "  tools_disabled: %s\n", strings.Join(pref.ToolsDisabled, ", "))
	}
	if pref.MCP != nil {
		if len(*pref.MCP) == 0 {
			fmt.Fprintln(w, "  mcp: []")
		} else {
			fmt.Fprintf(w, "  mcp: %s\n", strings.Join(*pref.MCP, ", "))
		}
	}

	// Shell settings
	if len(pref.ShellAllow) > 0 {
		fmt.Fprintf(w, "  shell_allow: %s\n", strings.Join(pref.ShellAllow, ", "))
	}
	if pref.ShellAutoRun != nil {
		fmt.Fprintf(w, "  shell_auto_run: %v\n", *pref.ShellAutoRun)
	}

	// Spawn settings
	if pref.SpawnMaxParallel != nil {
		fmt.Fprintf(w, "  spawn_max_parallel: %d\n", *pref.SpawnMaxParallel)
	}
	if pref.SpawnMaxDepth != nil {
		fmt.Fprintf(w, "  spawn_max_depth: %d\n", *pref.SpawnMaxDepth)
	}
	if pref.SpawnTimeout != nil {
		fmt.Fprintf(w, "  spawn_timeout: %d\n", *pref.SpawnTimeout)
	}
	if len(pref.SpawnAllowedAgents) > 0 {
		fmt.Fprintf(w, "  spawn_allowed_agents: %s\n", strings.Join(pref.SpawnAllowedAgents, ", "))
	}
	if len(pref.AllowedModels) > 0 {
		fmt.Fprintf(w, "  allowed_models: %s\n", strings.Join(pref.AllowedModels, ", "))
	}

	// Behavior
	if pref.MaxTurns != nil {
		fmt.Fprintf(w, "  max_turns: %d\n", *pref.MaxTurns)
	}
	if pref.Search != nil {
		fmt.Fprintf(w, "  search: %v\n", *pref.Search)
	}
}
