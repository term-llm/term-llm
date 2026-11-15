package agents

import (
	"reflect"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
)

func TestMergeMCPPreference(t *testing.T) {
	empty := []string{}
	names := []string{"github", "playwright"}
	defaults := []MCPConfig{{Name: "default", Command: "old-command"}}
	for _, tt := range []struct {
		name string
		pref *[]string
		want []MCPConfig
	}{
		{name: "absent preserves defaults", want: defaults},
		{name: "empty clears defaults", pref: &empty, want: []MCPConfig{}},
		{name: "names replace defaults without commands", pref: &names, want: []MCPConfig{{Name: "github"}, {Name: "playwright"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{MCP: append([]MCPConfig(nil), defaults...)}
			a.Merge(config.AgentPreference{MCP: tt.pref})
			if !reflect.DeepEqual(a.MCP, tt.want) {
				t.Fatalf("MCP = %+v, want %+v", a.MCP, tt.want)
			}
			a.Merge(config.AgentPreference{})
			if !reflect.DeepEqual(a.MCP, tt.want) {
				t.Fatalf("absent preference changed MCP: %+v", a.MCP)
			}
		})
	}
}

func TestMergeMCPPreferenceDoesNotAlias(t *testing.T) {
	names := []string{"github", "playwright"}
	pref := config.AgentPreference{MCP: &names}
	defaults := []MCPConfig{{Name: "default"}}
	a, b := &Agent{MCP: defaults}, &Agent{MCP: defaults}
	a.Merge(pref)
	b.Merge(pref)
	names[0] = "changed-preference"
	if a.MCP[0].Name != "github" || b.MCP[0].Name != "github" {
		t.Fatal("merged agent aliases preference")
	}
	a.MCP[0].Name = "changed-agent"
	if b.MCP[0].Name != "github" || defaults[0].Name != "default" || names[0] != "changed-preference" {
		t.Fatal("merged agent aliases another agent, the definition, or preference")
	}
}

func TestRegistryBuiltinMCPPreference(t *testing.T) {
	// Use only builtins, independent of any configured/user agent directories.
	r := &Registry{useBuiltin: true, cache: make(map[string]*Agent)}
	original, err := r.Get("codebase")
	if err != nil {
		t.Fatal(err)
	}
	defaults := append([]MCPConfig(nil), original.MCP...)
	names := []string{"github", "playwright"}
	r.SetPreferences(map[string]config.AgentPreference{"codebase": {MCP: &names}})
	a, err := r.Get("codebase")
	if err != nil {
		t.Fatal(err)
	}
	if a.Source != SourceBuiltin || !reflect.DeepEqual(a.GetMCPServerNames(), names) {
		t.Fatalf("builtin override: source = %v, MCP = %+v", a.Source, a.MCP)
	}
	if !reflect.DeepEqual(original.MCP, defaults) {
		t.Fatal("preference changed original builtin")
	}
	empty := []string{}
	r.SetPreferences(map[string]config.AgentPreference{"codebase": {MCP: &empty}})
	a, err = r.Get("codebase")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.MCP) != 0 {
		t.Fatalf("empty preference did not clear builtin MCP: %+v", a.MCP)
	}
	r.SetPreferences(nil)
	a, err = r.Get("codebase")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.MCP, defaults) {
		t.Fatalf("removing preferences did not restore builtin defaults: %+v", a.MCP)
	}
}
