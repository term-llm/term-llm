package config

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAgentMCPPreferenceLoadAndYAMLRoundTrip(t *testing.T) {
	empty := []string{}
	names := []string{"GitHub", "playwright"}
	for _, tt := range []struct {
		name  string
		field string
		want  *[]string
	}{
		{name: "absent"},
		{name: "null", field: "    mcp: null\n"},
		{name: "empty", field: "    mcp: []\n", want: &empty},
		{name: "names", field: "    mcp: [GitHub, playwright]\n", want: &names},
	} {
		t.Run(tt.name, func(t *testing.T) {
			configHome := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", configHome)
			path := writeTestConfig(t, configHome, "agents:\n preferences:\n  developer:\n    model: test-model\n"+tt.field)
			cfg, err := loadUnresolved()
			if err != nil {
				t.Fatal(err)
			}
			pref := cfg.Agents.Preferences["developer"]
			if !reflect.DeepEqual(pref.MCP, tt.want) {
				t.Fatalf("MCP = %v, want %v", pref.MCP, tt.want)
			}

			data, err := yaml.Marshal(pref)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == nil && strings.Contains(string(data), "mcp:") {
				t.Fatalf("absent MCP was serialized: %s", data)
			}
			if tt.want != nil && len(*tt.want) == 0 && !strings.Contains(string(data), "mcp: []") {
				t.Fatalf("explicit empty MCP was omitted: %s", data)
			}
			var roundTrip AgentPreference
			if err := yaml.Unmarshal(data, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roundTrip, pref) {
				t.Fatalf("YAML round trip = %+v, want %+v", roundTrip, pref)
			}

			// Exercise the file/mapstructure decoder with the marshaled preference too.
			document, err := yaml.Marshal(map[string]any{"agents": map[string]any{"preferences": map[string]AgentPreference{"developer": pref}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, document, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err = loadUnresolved()
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Agents.Preferences["developer"]; !reflect.DeepEqual(got, pref) {
				t.Fatalf("file round trip = %+v, want %+v", got, pref)
			}
		})
	}
}

func TestSetAgentMCPPreference(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	path := writeTestConfig(t, configHome, "# keep comment\nagents:\n  preferences:\n    other:\n      mcp: [existing]\n")
	if !KnownAgentPreferenceKeys["mcp"] || !IsKnownKey("agents.preferences.developer.mcp") {
		t.Fatal("MCP must be a known agent preference key")
	}
	for _, tt := range []struct {
		value string
		want  []string
	}{
		{value: "github, playwright", want: []string{"github", "playwright"}},
		{value: "", want: []string{}},
	} {
		t.Run(tt.value, func(t *testing.T) {
			keys, err := SetAgentPreference("developer", "mcp", tt.value)
			if err != nil || !reflect.DeepEqual(keys, []string{"mcp"}) {
				t.Fatalf("SetAgentPreference = %v, %v", keys, err)
			}
			cfg, err := loadUnresolved()
			if err != nil {
				t.Fatal(err)
			}
			pref := cfg.Agents.Preferences["developer"]
			if pref.MCP == nil || !reflect.DeepEqual(*pref.MCP, tt.want) {
				t.Fatalf("MCP = %v, want %v", pref.MCP, tt.want)
			}
			// A subsequent unrelated edit must preserve explicit empty overrides.
			if _, err := SetAgentPreference("developer", "model", "test-model"); err != nil {
				t.Fatal(err)
			}
			cfg, err = loadUnresolved()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.Agents.Preferences["developer"].MCP, pref.MCP) {
				t.Fatal("unrelated edit changed MCP preference")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "# keep comment") {
				t.Fatalf("comment lost: %s", data)
			}
			if len(tt.want) == 0 && !strings.Contains(string(data), "mcp: []") {
				t.Fatalf("empty MCP must be written as []: %s", data)
			}
		})
	}
}
