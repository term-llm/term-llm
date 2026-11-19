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

const sofyaFixture = `{"query":"hello","answer":null,"results":[{"title":"Result 1","url":"https://example.com/1","content":"Page text 1","description":"Snippet 1","fetched":false},{"title":"No URL","url":"","description":"dropped"},{"title":"Result 2","url":"https://example.com/2","content":"Content fallback","description":""}],"search_depth":"snippets","credits_used":1}`

func newSofyaTestServer(t *testing.T, check func(r *http.Request, body sofyaRequest)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		var body sofyaRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		check(r, body)
		fmt.Fprint(w, sofyaFixture)
	}))
}

func TestSofyaSearcherSearch(t *testing.T) {
	var gotURL string
	ts := newSofyaTestServer(t, func(r *http.Request, body sofyaRequest) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		if body.Query != "hello" || body.SearchDepth != "snippets" || body.MaxResults != 3 {
			t.Fatalf("body = %+v", body)
		}
	})
	defer ts.Close()

	searcher := NewSofyaSearcher(" test-key ", ts.Client())
	searcher.client.Transport = recordingRewriteTransport{base: ts.Client().Transport, target: ts.URL, gotURL: &gotURL}

	results, err := searcher.Search(context.Background(), "hello", 3)
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if want := sofyaBaseURL + "/search"; gotURL != want {
		t.Fatalf("url = %q, want %q", gotURL, want)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (entry without URL dropped)", len(results))
	}
	if results[0].Title != "Result 1" || results[0].URL != "https://example.com/1" || results[0].Snippet != "Snippet 1" {
		t.Fatalf("results[0] = %+v", results[0])
	}
	if results[1].Snippet != "Content fallback" {
		t.Fatalf("results[1].Snippet = %q, want content fallback", results[1].Snippet)
	}
}

func TestSofyaSearcherMaxResults(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 10}, {50, 20}, {1, 1}} {
		ts := newSofyaTestServer(t, func(_ *http.Request, body sofyaRequest) {
			if body.MaxResults != tc.want {
				t.Fatalf("max_results for %d = %d, want %d", tc.in, body.MaxResults, tc.want)
			}
		})
		searcher := NewSofyaSearcher("test-key", ts.Client())
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

func TestSofyaSearcherStatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		fmt.Fprint(w, `{"error":"insufficient_credits","detail":"Insufficient credits"}`)
	}))
	defer ts.Close()

	searcher := NewSofyaSearcher("test-key", ts.Client())
	searcher.client.Transport = rewriteTransport{base: ts.Client().Transport, target: ts.URL}

	_, err := searcher.Search(context.Background(), "hello", 5)
	var statusErr *providerhttp.StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("Search error = %T %v, want 402 *providerhttp.StatusError", err, err)
	}
	if !strings.Contains(err.Error(), "insufficient_credits") {
		t.Fatalf("Search error = %q, want the API message", err)
	}
}

func TestSofyaSearcherMalformedSuccessResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":`)
	}))
	defer ts.Close()

	searcher := NewSofyaSearcher("test-key", ts.Client())
	searcher.client.Transport = rewriteTransport{base: ts.Client().Transport, target: ts.URL}
	if _, err := searcher.Search(context.Background(), "hello", 2); err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Fatalf("Search error = %v, want parse response error", err)
	}
}

func TestSofyaSearcherEmptyQuery(t *testing.T) {
	if _, err := NewSofyaSearcher("test-key", nil).Search(context.Background(), "  ", 5); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSofyaFactoryRequiresKey(t *testing.T) {
	t.Setenv("SOFYA_API_KEY", "")
	cfg := &config.Config{}
	cfg.Search.Provider = "sofya"
	if err := Available(cfg); err == nil || !strings.Contains(err.Error(), "SOFYA_API_KEY") {
		t.Fatalf("Available() = %v, want SOFYA_API_KEY error", err)
	}
	if _, err := NewSearcher(cfg); err == nil || !strings.Contains(err.Error(), "SOFYA_API_KEY") {
		t.Fatalf("NewSearcher() = %v, want SOFYA_API_KEY error", err)
	}
}

func TestSofyaFactoryCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, configured, env, want string
	}{
		{name: "environment", env: "env-key", want: "env-key"},
		{name: "configured", configured: "config-key", env: "env-key", want: "config-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SOFYA_API_KEY", tc.env)
			cfg := &config.Config{}
			cfg.Search.Provider = "sofya"
			cfg.Search.Sofya.APIKey = tc.configured
			if err := Available(cfg); err != nil {
				t.Fatalf("Available() = %v", err)
			}
			s, err := NewSearcher(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := s.(*SofyaSearcher).apiKey; got != tc.want {
				t.Fatalf("apiKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSofyaFactoryCredentialError(t *testing.T) {
	cfg := &config.Config{}
	cfg.Search.Provider = "sofya"
	cfg.Search.Sofya.APIKey = "file://" + t.TempDir() + "/missing-key"
	if err := Available(cfg); err != nil {
		t.Fatalf("Available must not resolve credentials: %v", err)
	}
	if _, err := NewSearcher(cfg); err == nil || !strings.Contains(err.Error(), "search.sofya.api_key") {
		t.Fatalf("NewSearcher error = %v, want credential resolution error", err)
	}
}

// TestSofyaSearcherLive calls the real API.
// Run with SOFYA_LIVE=1 SOFYA_API_KEY=... go test ./internal/search -run Live.
func TestSofyaSearcherLive(t *testing.T) {
	if os.Getenv("SOFYA_LIVE") == "" {
		t.Skip("set SOFYA_LIVE=1 and SOFYA_API_KEY to call the live API")
	}
	results, err := NewSofyaSearcher(os.Getenv("SOFYA_API_KEY"), nil).Search(context.Background(), "golang context package", 3)
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	if len(results) == 0 || !strings.HasPrefix(results[0].URL, "http") || results[0].Snippet == "" {
		t.Fatalf("results = %+v", results)
	}
}
