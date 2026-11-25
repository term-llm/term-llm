package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/providerhttp"
)

const webzFixture = `{"query":"hello","total_results":2,"results":[{"score":8.5,"article":{"article_id":"a1","url":"https://example.com/1","title":"Result 1","published_at":"2026-11-20T00:00:00Z","summary":"Summary 1"},"chunk":{"chunk_id":"c1","text":"Snippet 1"},"metadata":{"domain":"example.com"}},{"score":1,"article":{"url":"","title":"No URL","summary":"dropped"},"chunk":{"text":"ignored"}},{"score":7,"article":{"url":"https://example.com/2","title":"Result 2","summary":"Summary fallback"},"chunk":{"text":"  "}}]}`

func newWebzTestServer(t *testing.T, check func(r *http.Request, body map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if _, ok := body["days"]; ok {
			t.Fatal("request sent a days field")
		}
		check(r, body)
		fmt.Fprint(w, webzFixture)
	}))
}

func TestWebzSearcherSearch(t *testing.T) {
	var gotURL string
	ts := newWebzTestServer(t, func(r *http.Request, body map[string]any) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		if body["query"] != "hello" || body["k"] != float64(3) {
			t.Fatalf("body = %+v", body)
		}
		if _, ok := body["filters"]; ok {
			t.Fatalf("body = %+v, want no filters object", body)
		}
	})
	defer ts.Close()

	searcher := NewWebzSearcher(" test-key ", ts.Client())
	searcher.client.Transport = recordingRewriteTransport{base: ts.Client().Transport, target: ts.URL, gotURL: &gotURL}

	results, err := searcher.Search(context.Background(), "hello", 3)
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if want := webzBaseURL + webzSearchPath; gotURL != want {
		t.Fatalf("url = %q, want %q", gotURL, want)
	}
	if strings.Contains(gotURL, "test-key") {
		t.Fatal("token was placed in the request URL")
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (entry without URL dropped)", len(results))
	}
	if results[0].Title != "Result 1" || results[0].URL != "https://example.com/1" || results[0].Snippet != "Snippet 1" {
		t.Fatalf("results[0] = %+v", results[0])
	}
	if results[1].Snippet != "Summary fallback" {
		t.Fatalf("results[1].Snippet = %q, want summary fallback", results[1].Snippet)
	}
}

func TestWebzSearcherMaxResults(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 10}, {50, 20}, {1, 1}} {
		ts := newWebzTestServer(t, func(_ *http.Request, body map[string]any) {
			if body["k"] != float64(tc.want) {
				t.Fatalf("k for %d = %v, want %d", tc.in, body["k"], tc.want)
			}
		})
		searcher := NewWebzSearcher("test-key", ts.Client())
		searcher.client.Transport = rewriteTransport{base: ts.Client().Transport, target: ts.URL}
		results, err := searcher.Search(context.Background(), "hello", tc.in)
		ts.Close()
		if err != nil {
			t.Fatalf("Search error = %v", err)
		}
		if tc.in == 1 && len(results) != 1 {
			t.Fatalf("len(results) = %d, want 1", len(results))
		}
	}
}

func TestWebzSearcherRejectsOverlongQuery(t *testing.T) {
	searcher := NewWebzSearcher("test-key", nil)
	if _, err := searcher.Search(context.Background(), strings.Repeat("a", webzMaxQueryChars+1), 1); err == nil || !strings.Contains(err.Error(), "characters") {
		t.Fatalf("Search error = %v, want a character-limit error", err)
	}
	words := strings.Repeat("word ", webzMaxQueryWords+1)
	if _, err := searcher.Search(context.Background(), words, 1); err == nil || !strings.Contains(err.Error(), "words") {
		t.Fatalf("Search error = %v, want a word-limit error", err)
	}
}

func TestWebzSearcherStatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"detail":"invalid token"}`)
	}))
	defer ts.Close()

	searcher := NewWebzSearcher("test-key", ts.Client())
	searcher.client.Transport = rewriteTransport{base: ts.Client().Transport, target: ts.URL}

	_, err := searcher.Search(context.Background(), "hello", 5)
	var statusErr *providerhttp.StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Search error = %T %v, want 401 *providerhttp.StatusError", err, err)
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("Search error = %q, want the API message", err)
	}
}

func TestWebzSearcherMalformedSuccessResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":`)
	}))
	defer ts.Close()

	searcher := NewWebzSearcher("test-key", ts.Client())
	searcher.client.Transport = rewriteTransport{base: ts.Client().Transport, target: ts.URL}
	if _, err := searcher.Search(context.Background(), "hello", 2); err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Fatalf("Search error = %v, want parse response error", err)
	}
}

func TestWebzSearcherEmptyQuery(t *testing.T) {
	if _, err := NewWebzSearcher("test-key", nil).Search(context.Background(), "  ", 5); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestWebzFactoryRequiresKey(t *testing.T) {
	t.Setenv("WEBZ_API_TOKEN", "")
	cfg := &config.Config{}
	cfg.Search.Provider = "webz"
	if err := Available(cfg); err == nil || !strings.Contains(err.Error(), "WEBZ_API_TOKEN") {
		t.Fatalf("Available() = %v, want WEBZ_API_TOKEN error", err)
	}
	if _, err := NewSearcher(cfg); err == nil || !strings.Contains(err.Error(), "WEBZ_API_TOKEN") {
		t.Fatalf("NewSearcher() = %v, want WEBZ_API_TOKEN error", err)
	}
}

func TestWebzFactoryCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, configured, env, want string
	}{
		{name: "environment", env: "env-key", want: "env-key"},
		{name: "configured", configured: "config-key", env: "env-key", want: "config-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEBZ_API_TOKEN", tc.env)
			cfg := &config.Config{}
			cfg.Search.Provider = "webz"
			cfg.Search.Webz.APIKey = tc.configured
			if err := Available(cfg); err != nil {
				t.Fatalf("Available() = %v", err)
			}
			s, err := NewSearcher(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.(*WebzSearcher).apiKey; got != tc.want {
				t.Fatalf("apiKey = %q, want %q", got, tc.want)
			}
			if got := s.(*WebzSearcher).baseURL; got != webzBaseURL {
				t.Fatalf("baseURL = %q, want %q", got, webzBaseURL)
			}
		})
	}
}

func TestWebzFactoryCredentialError(t *testing.T) {
	cfg := &config.Config{}
	cfg.Search.Provider = "webz"
	cfg.Search.Webz.APIKey = "file://" + t.TempDir() + "/missing-key"
	if err := Available(cfg); err != nil {
		t.Fatalf("Available must not resolve credentials: %v", err)
	}
	if _, err := NewSearcher(cfg); err == nil || !strings.Contains(err.Error(), "search.webz.api_key") {
		t.Fatalf("NewSearcher error = %v, want credential resolution error", err)
	}
}

// TestWebzSearcherLive calls the real API.
// Run with WEBZ_LIVE=1 WEBZ_API_TOKEN=... go test ./internal/search -run Live.
func TestWebzSearcherLive(t *testing.T) {
	if os.Getenv("WEBZ_LIVE") == "" {
		t.Skip("set WEBZ_LIVE=1 and WEBZ_API_TOKEN to call the live API")
	}
	results, err := NewWebzSearcher(os.Getenv("WEBZ_API_TOKEN"), nil).Search(context.Background(), "golang context package", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].URL == "" {
		t.Fatalf("results = %+v", results)
	}
}
