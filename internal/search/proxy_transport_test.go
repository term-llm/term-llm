package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

func TestDelegatedSearchDoesNotFollowRedirects(t *testing.T) {
	const token = "test-search-scoped-token"
	for _, tc := range []struct{ provider, path, auth, prefix string }{
		{"brave", "/search/web/search", "X-Subscription-Token", ""},
		{"exa_mcp", "/search/mcp", "X-API-Key", ""},
		{"sofya", "/search/search", "Authorization", "Bearer "},
		{"webz", "/search/api/news/context", "Authorization", "Bearer "},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			var first atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("search followed redirect to %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				first.Add(1)
				if r.Header.Get(tc.auth) != tc.prefix+token || tc.auth != "Authorization" && r.Header.Get("Authorization") != "" {
					t.Error("search did not send exactly its expected caller auth header")
				}
				w.Header().Set("Location", "/would-be-public")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			if err := proxyclient.Register([]proxyclient.Binding{{Root: server.URL + "/search", Token: token}}); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Search: config.SearchConfig{
				Provider: tc.provider,
				Brave:    config.SearchBraveConfig{APIKey: token, BaseURL: server.URL + "/search"},
				ExaMCP:   config.SearchExaMCPConfig{URL: server.URL + "/search/mcp", APIKey: token},
				Sofya:    config.SearchSofyaConfig{APIKey: token, BaseURL: server.URL + "/search"},
				Webz:     config.SearchWebzConfig{APIKey: token, BaseURL: server.URL + "/search"},
			}}
			backend, err := NewSearcher(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := backend.Search(context.Background(), "search term", 2); err == nil || first.Load() == 0 {
				t.Fatalf("expected a rejected redirect: err=%v, requests=%d", err, first.Load())
			}
		})
	}
}
