package webagents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestDraftApprovalActionsHonorMCPPreferences(t *testing.T) {
	cfg := testConfig(t)
	writeTestAgent(t, cfg, "enabled", "")
	writeTestAgent(t, cfg, "disabled", "mcp: [{name: github}]\n")
	mcpPath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "term-llm", "mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{"servers":{"github":{"command":"must-not-start"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	names, empty := []string{"github"}, []string{}
	cfg.Agents.Preferences = map[string]config.AgentPreference{
		"enabled":  {MCP: &names},
		"disabled": {MCP: &empty},
	}
	actions := DraftApprovalActions(cfg, "", false, "")
	if governed, known := actions["enabled"]; known {
		t.Fatalf("undiscovered preference-selected MCP capability = %v; want unknown without starting server", governed)
	}
	if governed, known := actions["disabled"]; !known || governed {
		t.Fatalf("disabled default capability = %v (known %v); want known no approval actions", governed, known)
	}
	if governed, known := actions[""]; !known || governed {
		t.Fatalf("Chat inherited agent defaults: %v (known %v)", governed, known)
	}
}
