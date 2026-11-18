package clibin

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bridgeAuth struct {
	header string
	base   http.RoundTripper
}

func (a bridgeAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", a.header)
	return a.base.RoundTrip(req)
}

// bridgeConfigTools returns, by name, the tools a CLI discovers through the
// term-llm bridge entry of an MCP config file written for its turn. The entry
// must use the url/headers keys of Claude Code and Cursor configs.
func bridgeConfigTools(t *testing.T, configPath string) map[string]*mcp.Tool {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read MCP config: %v", err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode MCP config: %v", err)
	}
	bridge := cfg.MCPServers["term-llm"]
	// A private transport whose connections are all closed afterwards: a spare
	// keep-alive connection left open would hold up the server's later Stop.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             bridge.URL,
		HTTPClient:           &http.Client{Transport: bridgeAuth{header: bridge.Headers["Authorization"], base: transport}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect to bridge: %v", err)
	}
	defer session.Close()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list bridge tools: %v", err)
	}
	tools := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func bridgeConfigToolNames(t *testing.T, configPath string) []string {
	t.Helper()
	names := make([]string, 0)
	for name := range bridgeConfigTools(t, configPath) {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
