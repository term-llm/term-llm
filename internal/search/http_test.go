package search

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/providerhttp"
)

func TestAuthenticatedSearchRefusesRedirects(t *testing.T) {
	const key = "fixture-vendor-key"
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != key && r.Header.Get("X-Subscription-Token") != key &&
			r.Header.Get("Authorization") != "Bearer "+key && r.URL.Query().Get("key") != key {
			t.Error("initial request did not use its API key")
		}
		// A credentialed search should not follow even a same-host redirect.
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	for _, provider := range []string{"brave", "exa", "exa_mcp", "google", "keenable", "parallel", "perplexity", "sofya", "tavily", "webz", "youcom"} {
		t.Run(provider, func(t *testing.T) {
			cfg := &config.Config{Search: config.SearchConfig{
				Provider:   provider,
				Brave:      config.SearchBraveConfig{APIKey: key, BaseURL: source.URL},
				Exa:        config.SearchExaConfig{APIKey: key, BaseURL: source.URL},
				ExaMCP:     config.SearchExaMCPConfig{APIKey: key, URL: source.URL + "/mcp"},
				Google:     config.SearchGoogleConfig{APIKey: key, BaseURL: source.URL, CX: "test-cx"},
				Keenable:   config.SearchKeenableConfig{APIKey: key, BaseURL: source.URL},
				Parallel:   config.SearchParallelConfig{APIKey: key, BaseURL: source.URL},
				Perplexity: config.SearchPerplexityConfig{APIKey: key, BaseURL: source.URL},
				Sofya:      config.SearchSofyaConfig{APIKey: key, BaseURL: source.URL},
				Tavily:     config.SearchTavilyConfig{APIKey: key, BaseURL: source.URL},
				Webz:       config.SearchWebzConfig{APIKey: key, BaseURL: source.URL},
				YouCom:     config.SearchYouComConfig{APIKey: key, BaseURL: source.URL},
			}}
			backend, err := NewSearcher(cfg)
			if err != nil {
				t.Fatal(err)
			}
			before := received.Load()
			_, err = backend.Search(t.Context(), "search term", 2)
			if err == nil || received.Load() != before {
				t.Fatalf("expected no request to the redirect target; err=%v, requests=%d", err, received.Load()-before)
			}
			if provider != "exa_mcp" {
				var status *providerhttp.StatusError
				if !errors.As(err, &status) || status.StatusCode != http.StatusTemporaryRedirect {
					t.Fatalf("expected a typed 307 error, got %v", err)
				}
			}
		})
	}
}

func TestSearchResponseSizeBounds(t *testing.T) {
	t.Run("oversize success", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxSearchResponseBytes+1)))}
		if _, err := readSearchResponse(resp); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("expected size limit error, got %v", err)
		}
	})
	t.Run("oversize provider error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, strings.Repeat("x", maxSearchErrorBytes+1))
		}))
		defer server.Close()
		backend := NewSofyaSearcher("fixture-key", server.Client())
		backend.baseURL = server.URL
		_, err := backend.Search(t.Context(), "search term", 2)
		var status *providerhttp.StatusError
		if !errors.As(err, &status) || status.StatusCode != 429 || len(status.Body) != maxSearchErrorBytes {
			t.Fatal("expected a typed 429 with bounded diagnostics")
		}
		if delay, ok := status.RetryAfterDelay(); !ok || delay != 5*time.Second {
			t.Fatalf("Retry-After = %v, %v", delay, ok)
		}
	})
}

func TestGoogleSearchPreservesStatus(t *testing.T) {
	for _, body := range []string{`{"error":{"code":429,"message":"google quota"}}`, "google quota"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "10")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, body)
		}))
		backend := NewGoogleSearcher("fixture-key", "test-cx", server.Client())
		backend.baseURL = server.URL
		_, err := backend.Search(t.Context(), "search term", 2)
		server.Close()
		var status *providerhttp.StatusError
		if !errors.As(err, &status) || status.StatusCode != 429 || !strings.Contains(status.Error(), "google quota") {
			t.Fatalf("expected Google 429 status and message, got %v", err)
		}
		if delay, ok := status.RetryAfterDelay(); !ok || delay != 10*time.Second {
			t.Fatalf("Retry-After = %v, %v", delay, ok)
		}
	}
}

func TestGoogleSearchDoesNotExposeKeyOnTransportError(t *testing.T) {
	const key = "fixture-secret-key"
	server := httptest.NewServer(http.NotFoundHandler())
	backend := NewGoogleSearcher(key, "test-cx", server.Client())
	backend.baseURL = server.URL
	server.Close()
	if _, err := backend.Search(t.Context(), "search term", 2); err == nil || strings.Contains(err.Error(), key) {
		t.Fatal("expected a transport error without the API key")
	}
}
