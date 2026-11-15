package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClearAgentPreferenceKeys(t *testing.T) {
	empty := []string{}
	for _, tc := range []struct {
		name   string
		keys   []string
		want   AgentPreference
		pruned bool
	}{
		{name: "MCP only", keys: []string{"mcp"}, want: AgentPreference{Model: "preferred"}},
		{name: "model only", keys: []string{"model"}, want: AgentPreference{MCP: &empty}},
		{name: "multiple keys", keys: []string{"mcp", "model"}, pruned: true},
		{name: "all", pruned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", home)
			path := writeTestConfig(t, home, "# keep config\nagents:\n  use_builtin: true # keep setting\n  preferences:\n    my.agent:\n      model: preferred # keep model\n      mcp: []\n    other:\n      model: untouched\n")
			if err := ClearAgentPreferences("my.agent", tc.keys...); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadUnresolved()
			if err != nil {
				t.Fatal(err)
			}
			pref, found := cfg.Agents.Preferences["my.agent"]
			if !reflect.DeepEqual(pref, tc.want) {
				t.Fatalf("remaining preference = %+v, want %+v", pref, tc.want)
			}
			if found == tc.pruned {
				t.Fatalf("agent preference found = %v, want %v", found, !tc.pruned)
			}
			if cfg.Agents.Preferences["other"].Model != "untouched" || !cfg.Agents.UseBuiltin {
				t.Fatal("clearing keys changed another agent or setting")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, comment := range []string{"# keep config", "# keep setting"} {
				if !strings.Contains(string(data), comment) {
					t.Fatalf("lost comment %q: %s", comment, data)
				}
			}
			if tc.want.Model != "" && !strings.Contains(string(data), "# keep model") {
				t.Fatal("clearing MCP lost an unrelated comment")
			}
		})
	}
}

func TestClearAgentPreferenceKeysNoOpAndValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	path := writeTestConfig(t, home, "# preserve bytes\nagents: {preferences: {developer: {mcp: []}}}\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		agent string
		keys  []string
		bad   bool
	}{
		{agent: "missing", keys: []string{"mcp"}},
		{agent: "developer", keys: []string{"model"}},
		{agent: "developer", keys: []string{"mcp", "typo"}, bad: true},
	} {
		err := ClearAgentPreferences(tc.agent, tc.keys...)
		if (err != nil) != tc.bad || (tc.bad && !strings.Contains(err.Error(), "unknown agent preference key: typo")) {
			t.Fatalf("clear %v: unexpected error %v", tc.keys, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Fatalf("no-op or rejected edit changed config: %s, %v", after, err)
		}
	}
}

func TestClearLastAgentPreferenceKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	path := writeTestConfig(t, home, "agents:\n  use_builtin: true\n  preferences:\n    developer:\n      mcp: []\n")
	if err := ClearAgentPreferences("developer", "mcp"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "preferences:") || !strings.Contains(string(data), "use_builtin: true") {
		t.Fatalf("empty preference mappings not pruned: %s, %v", data, err)
	}
}

func TestClearAgentPreferenceKeyWithoutConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	if err := ClearAgentPreferences("developer", "mcp"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "term-llm", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("clearing absent preference created a file: %v", err)
	}
}
