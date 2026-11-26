package opencompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/cache"
	"github.com/term-llm/term-llm/internal/config"
)

func TestNewTrustedRouterProviderNormalizesAPIKey(t *testing.T) {
	for _, raw := range []string{"  test-key\n", "  Bearer test-key\n"} {
		provider := NewTrustedRouterProvider(raw, "")
		if provider.apiKey != "test-key" {
			t.Fatalf("apiKey from %q = %q, want %q", raw, provider.apiKey, "test-key")
		}
	}
}

func TestNewTrustedRouterProviderDefaults(t *testing.T) {
	provider := NewTrustedRouterProvider("tr-key", "")
	if provider.model != "trustedrouter/auto" {
		t.Fatalf("model = %q, want trustedrouter/auto", provider.model)
	}
	if provider.baseURL != "https://api.trustedrouter.com/v1" {
		t.Fatalf("baseURL = %q, want https://api.trustedrouter.com/v1", provider.baseURL)
	}
	if got := config.DefaultProviderFastModel("trustedrouter"); got != "trustedrouter/fast" {
		t.Fatalf("fast_model = %q, want trustedrouter/fast", got)
	}
}

func TestTrustedRouterProviderCapabilities(t *testing.T) {
	caps := NewTrustedRouterProvider("key", "").Capabilities()
	if caps.NativeWebSearch || caps.NativeWebFetch {
		t.Fatalf("expected no native web tools, got %+v", caps)
	}
	if !caps.ToolCalls || !caps.SupportsToolChoice {
		t.Fatalf("expected tool calls with tool_choice, got %+v", caps)
	}
}

func TestTrustedRouterListModelsParsesAndFiltersLiveCatalog(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	boolPtr := func(v bool) *bool { return &v }
	rows := []trustedRouterModel{
		{
			ID:            "trustedrouter/auto",
			Name:          "TrustedRouter Auto",
			ContextLength: 1048576,
			Pricing:       &oaiModelPricing{Prompt: 0.000000026375, Completion: 0.00000007385},
			TrustedRouter: &trustedRouterModelMetadata{
				Provider:     "trustedrouter",
				SupportsChat: boolPtr(true),
				Aliases:      []string{"nyte/auto"},
			},
		},
		{
			ID:            "trustedrouter/zdr",
			Name:          "TrustedRouter ZDR",
			ContextLength: 1048576,
			Pricing:       &oaiModelPricing{Prompt: 0.00000003165, Completion: 0.0000001266},
			TrustedRouter: &trustedRouterModelMetadata{Provider: "trustedrouter", SupportsChat: boolPtr(true), Aliases: []string{"nyte/zdr"}},
		},
		{
			ID:            "trustedrouter/e2e",
			Name:          "TrustedRouter E2E",
			ContextLength: 1048576,
			Pricing:       &oaiModelPricing{Prompt: 0.00000001055, Completion: 0.00000007385},
			TrustedRouter: &trustedRouterModelMetadata{Provider: "trustedrouter", SupportsChat: boolPtr(true), Aliases: []string{"nyte/e2e"}},
		},
		{
			ID:            "trustedrouter/fast",
			Name:          "TrustedRouter Fast",
			ContextLength: 131072,
			Pricing:       &oaiModelPricing{Prompt: 0.00000036925, Completion: 0.00000079125},
			TrustedRouter: &trustedRouterModelMetadata{Provider: "trustedrouter", SupportsChat: boolPtr(true), Aliases: []string{"nyte/fast"}},
		},
		{
			ID:            "trustedrouter/cheap",
			Name:          "TrustedRouter Cheap",
			ContextLength: 1048576,
			Pricing:       &oaiModelPricing{Prompt: 0.00000001, Completion: 0.00000001},
			TrustedRouter: &trustedRouterModelMetadata{Provider: "trustedrouter", SupportsChat: boolPtr(true), Aliases: []string{"nyte/cheap"}},
		},
		{
			ID:            "trustedrouter/free",
			Name:          "TrustedRouter Free",
			ContextLength: 128000,
			Pricing:       &oaiModelPricing{Prompt: 0, Completion: 0},
			TrustedRouter: &trustedRouterModelMetadata{Provider: "trustedrouter", SupportsChat: boolPtr(true), Aliases: []string{"nyte/free"}},
		},
		{
			ID:            "nyte/e2e",
			Name:          "Nyte E2E Preset",
			ContextLength: 1048576,
			Pricing:       &oaiModelPricing{Prompt: 0.00000001055, Completion: 0.00000007385},
			TrustedRouter: &trustedRouterModelMetadata{Provider: "trustedrouter", SupportsChat: boolPtr(true)},
		},
		{
			ID:            "anthropic/claude-sonnet-4.6",
			Name:          "Anthropic: Claude Sonnet 4.6",
			ContextLength: 1000000,
			TopProvider:   &trustedRouterTopProvider{MaxCompletionTokens: 128000},
			Pricing:       &oaiModelPricing{Prompt: 0.000003, Completion: 0.000015},
			TrustedRouter: &trustedRouterModelMetadata{
				Provider:     "anthropic",
				SupportsChat: boolPtr(true),
				Capabilities: &trustedRouterCapabilities{ReasoningEffort: []string{"none", "low", "medium", "high", "max"}},
			},
		},
		{
			// Duplicate canonical ID should be deduplicated.
			ID:            "anthropic/claude-sonnet-4.6",
			Name:          "Duplicate Claude",
			ContextLength: 200000,
			TrustedRouter: &trustedRouterModelMetadata{Provider: "anthropic", SupportsChat: boolPtr(true)},
		},
		{
			// Missing pricing should report -1/-1 rather than FREE.
			ID:            "vendor/unpriced-chat",
			Name:          "Unpriced Chat",
			TopProvider:   &trustedRouterTopProvider{ContextLength: 65536},
			TrustedRouter: &trustedRouterModelMetadata{Provider: "vendor", SupportsChat: boolPtr(true)},
		},
		// Rows that MUST be filtered out:
		{
			ID:            "internal/canary",
			TrustedRouter: &trustedRouterModelMetadata{InternalOnly: true, SupportsChat: boolPtr(true)},
		},
		{
			ID:            "trustedrouter/archimedes-1.0",
			TrustedRouter: &trustedRouterModelMetadata{ConfigurationHidden: true, SupportsChat: boolPtr(true)},
		},
		{
			ID:            "monitor/synthetic-probe",
			TrustedRouter: &trustedRouterModelMetadata{SyntheticMonitor: true, SupportsChat: boolPtr(true)},
		},
		{
			ID:            "trustedrouter/advisor",
			Architecture:  &trustedRouterArchitecture{Modality: "text->text", OutputModalities: []string{"text"}},
			TrustedRouter: &trustedRouterModelMetadata{SupportsChat: boolPtr(false)},
		},
		{
			ID:            "openai/text-embedding-3-large",
			Architecture:  &trustedRouterArchitecture{Modality: "text->embedding", OutputModalities: []string{"embedding"}},
			TrustedRouter: &trustedRouterModelMetadata{SupportsChat: boolPtr(false), SupportsEmbeddings: true},
		},
		{
			ID:            "black-forest-labs/flux-2-flex",
			Architecture:  &trustedRouterArchitecture{Modality: "text->image", OutputModalities: []string{"image"}},
			TrustedRouter: &trustedRouterModelMetadata{SupportsChat: boolPtr(false)},
		},
		{
			ID:            "cohere/rerank-4",
			Architecture:  &trustedRouterArchitecture{Modality: "text->scores", OutputModalities: []string{"scores"}},
			TrustedRouter: &trustedRouterModelMetadata{SupportsRerank: true},
		},
		{
			ID:            "decisionmodelsio/s1-pro",
			Architecture:  &trustedRouterArchitecture{Modality: "text->decision", OutputModalities: []string{"decision"}},
			TrustedRouter: &trustedRouterModelMetadata{SupportsDecide: true},
		},
		{
			ID:           "legacy/video-only",
			Architecture: &trustedRouterArchitecture{Modality: "text+image->video"},
		},
		{
			ID:           "legacy/image-output",
			Architecture: &trustedRouterArchitecture{OutputModalities: []string{"text", "image"}},
		},
		{
			ID: "   ",
		},
	}

	// Add 500 additional public chat models to verify live multi-hundred catalog handling.
	const extraPublicModels = 500
	for i := 0; i < extraPublicModels; i++ {
		rows = append(rows, trustedRouterModel{
			ID:            fmt.Sprintf("provider-%02d/model-%03d", i%25, i),
			Name:          fmt.Sprintf("Model %03d", i),
			ContextLength: 131072,
			Pricing:       &oaiModelPricing{Prompt: 0.0000005, Completion: 0.0000015},
			TrustedRouter: &trustedRouterModelMetadata{Provider: fmt.Sprintf("provider-%02d", i%25), SupportsChat: boolPtr(true)},
		})
	}

	payload, err := json.Marshal(trustedRouterModelsResponse{Data: rows})
	if err != nil {
		t.Fatalf("Marshal catalog: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("expected no Authorization header for public listing without key, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	provider := NewTrustedRouterProvider("", "")
	provider.SetBaseURL(srv.URL)
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	// 9 distinct kept rows above + 500 generated public chat models = 509.
	const wantCount = 9 + extraPublicModels
	if len(models) != wantCount {
		t.Fatalf("got %d models, want %d", len(models), wantCount)
	}

	byID := make(map[string]ModelInfo, len(models))
	for _, m := range models {
		byID[m.ID] = m
	}

	for _, wantPreset := range []string{
		"trustedrouter/auto",
		"trustedrouter/zdr",
		"trustedrouter/e2e",
		"trustedrouter/fast",
		"trustedrouter/cheap",
		"trustedrouter/free",
		"nyte/e2e",
		"anthropic/claude-sonnet-4.6",
		"provider-00/model-000",
		"provider-24/model-499",
	} {
		if _, ok := byID[wantPreset]; !ok {
			t.Fatalf("missing expected model %q", wantPreset)
		}
	}

	for _, excluded := range []string{
		"internal/canary",
		"trustedrouter/archimedes-1.0",
		"monitor/synthetic-probe",
		"trustedrouter/advisor",
		"openai/text-embedding-3-large",
		"black-forest-labs/flux-2-flex",
		"cohere/rerank-4",
		"decisionmodelsio/s1-pro",
		"legacy/video-only",
		"legacy/image-output",
	} {
		if _, ok := byID[excluded]; ok {
			t.Fatalf("excluded row %q should have been filtered out", excluded)
		}
	}

	auto := byID["trustedrouter/auto"]
	if auto.InputLimit != 1048576 || auto.OwnedBy != "trustedrouter" {
		t.Fatalf("trustedrouter/auto = %+v, want 1048576 input limit and ownedBy trustedrouter", auto)
	}
	if auto.InputPrice != 0.026375 || auto.OutputPrice != 0.07385 {
		t.Fatalf("trustedrouter/auto prices = %v/%v, want 0.026375/0.07385", auto.InputPrice, auto.OutputPrice)
	}

	zdr := byID["trustedrouter/zdr"]
	if zdr.InputPrice != 0.03165 || zdr.OutputPrice != 0.1266 {
		t.Fatalf("trustedrouter/zdr prices = %v/%v, want 0.03165/0.1266", zdr.InputPrice, zdr.OutputPrice)
	}

	free := byID["trustedrouter/free"]
	if free.InputPrice != 0 || free.OutputPrice != 0 || free.InputLimit != 128000 {
		t.Fatalf("trustedrouter/free = %+v, want free pricing and 128000 input limit", free)
	}

	claude := byID["anthropic/claude-sonnet-4.6"]
	if claude.InputLimit != 1000000 || claude.OutputLimit != 128000 || claude.OwnedBy != "anthropic" {
		t.Fatalf("claude metadata = %+v", claude)
	}
	if math.Abs(claude.InputPrice-3.0) > 1e-9 || math.Abs(claude.OutputPrice-15.0) > 1e-9 {
		t.Fatalf("claude prices = %v/%v, want 3/15", claude.InputPrice, claude.OutputPrice)
	}
	if !slices.Equal(claude.ReasoningEfforts, []string{"none", "low", "medium", "high", "max"}) {
		t.Fatalf("claude reasoning efforts = %v", claude.ReasoningEfforts)
	}

	unpriced := byID["vendor/unpriced-chat"]
	if unpriced.InputLimit != 65536 || unpriced.InputPrice != -1 || unpriced.OutputPrice != -1 {
		t.Fatalf("unpriced = %+v, want 65536 input limit and -1/-1 pricing", unpriced)
	}

	// Custom base_url listing must not populate the global public cache.
	if cached := GetCachedTrustedRouterModels(""); len(cached) != 0 {
		t.Fatalf("custom base_url unexpectedly populated global cache: %d entries", len(cached))
	}
}

func TestTrustedRouterListModelsErrors(t *testing.T) {
	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":{"message":"service unavailable"}}`, http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		provider := NewTrustedRouterProvider("key", "")
		provider.SetBaseURL(srv.URL)
		if _, err := provider.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "503") {
			t.Fatalf("ListModels error = %v, want 503 status error", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{not-json`)
		}))
		defer srv.Close()

		provider := NewTrustedRouterProvider("key", "")
		provider.SetBaseURL(srv.URL)
		if _, err := provider.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "parse TrustedRouter models") {
			t.Fatalf("ListModels error = %v, want parse error", err)
		}
	})
}

func TestTrustedRouterStreamRequestWiring(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	var gotStream bool
	var gotIncludeUsage bool
	var gotTools int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll: %v", err)
			return
		}
		var req oaiChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("Unmarshal chat request: %v", err)
			return
		}
		gotModel = req.Model
		gotStream = req.Stream
		gotIncludeUsage = req.StreamOptions != nil && req.StreamOptions.IncludeUsage
		gotTools = len(req.Tools)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Attested \"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"reply\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":4,\"total_tokens\":16}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	provider := NewTrustedRouterProvider("Bearer tr-secret-key", "trustedrouter/zdr")
	provider.SetBaseURL(srv.URL + "/v1")

	stream, err := provider.Stream(context.Background(), Request{
		Messages: []Message{UserText("hello")},
		Tools: []ToolSpec{{
			Name:        "read_file",
			Description: "Read a file",
			Schema:      map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var text strings.Builder
	var usage *Usage
	sawDone := false
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		switch ev.Type {
		case EventTextDelta:
			text.WriteString(ev.Text)
		case EventUsage:
			usage = ev.Use
		case EventDone:
			sawDone = true
		}
	}

	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer tr-secret-key" {
		t.Fatalf("Authorization = %q, want Bearer tr-secret-key", gotAuth)
	}
	if gotModel != "trustedrouter/zdr" || !gotStream || !gotIncludeUsage || gotTools != 1 {
		t.Fatalf("wire request = model:%q stream:%v include_usage:%v tools:%d", gotModel, gotStream, gotIncludeUsage, gotTools)
	}
	if text.String() != "Attested reply" || !sawDone {
		t.Fatalf("stream output = %q done=%v, want %q done=true", text.String(), sawDone, "Attested reply")
	}
	if usage == nil || usage.InputTokens != 12 || usage.OutputTokens != 4 {
		t.Fatalf("usage = %+v, want 12/4", usage)
	}
}

func TestTrustedRouterModelInfoCacheAndAliasInputLimits(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	RefreshTrustedRouterCacheSync([]ModelInfo{
		{ID: "trustedrouter/zdr", DisplayName: "TrustedRouter ZDR", InputLimit: 1048576, InputPrice: 0.03165, OutputPrice: 0.1266},
		{ID: "anthropic/claude-sonnet-4.6", DisplayName: "Claude Sonnet 4.6", InputLimit: 1000000, OutputLimit: 128000},
	})

	models := GetCachedTrustedRouterModelInfos("")
	if len(models) != 2 || models[0].ID != "trustedrouter/zdr" || models[1].ID != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("cached models = %+v", models)
	}
	ids := GetCachedTrustedRouterModels("")
	if !slices.Equal(ids, []string{"trustedrouter/zdr", "anthropic/claude-sonnet-4.6"}) {
		t.Fatalf("cached IDs = %v", ids)
	}
	if got := TrustedRouterCachedInputLimit("trustedrouter/zdr"); got != 1048576 {
		t.Fatalf("TrustedRouterCachedInputLimit(trustedrouter/zdr) = %d, want 1048576", got)
	}
	if got := TrustedRouterCachedInputLimit("nyte/zdr"); got != 1048576 {
		t.Fatalf("TrustedRouterCachedInputLimit(nyte/zdr) = %d, want 1048576", got)
	}
	if got := TrustedRouterCachedInputLimit("anthropic/claude-sonnet-4.6"); got != 1000000 {
		t.Fatalf("TrustedRouterCachedInputLimit(anthropic/claude-sonnet-4.6) = %d, want 1000000", got)
	}
}

func TestGetCachedTrustedRouterModelsStartsSingleBackgroundRefreshForStaleCache(t *testing.T) {
	origClient := defaultHTTPClient
	trustedRouterCacheRefreshInFlight.Store(false)
	defer func() {
		defaultHTTPClient = origClient
		trustedRouterCacheRefreshInFlight.Store(false)
	}()

	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	cacheDir := filepath.Join(cacheHome, "term-llm")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	staleCache := cache.ModelCache{
		Models:    []string{"trustedrouter/auto"},
		FetchedAt: time.Now().Add(-cache.ModelCacheTTL - time.Minute),
	}
	cacheData, err := json.Marshal(staleCache)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, trustedRouterCacheKey+"-models.json"), cacheData, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var requests int32
	release := make(chan struct{})
	started := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		atomic.AddInt32(&requests, 1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"trustedrouter/zdr","context_length":1048576,"trustedrouter":{"supports_chat":true}}]}`)
	}))
	defer ts.Close()

	serverURL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	defaultHTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			cloned := req.Clone(req.Context())
			urlCopy := *req.URL
			cloned.URL = &urlCopy
			cloned.URL.Scheme = serverURL.Scheme
			cloned.URL.Host = serverURL.Host
			return ts.Client().Transport.RoundTrip(cloned)
		}),
	}

	const callers = 20
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := GetCachedTrustedRouterModels("test-key")
			if len(got) != 1 || got[0] != "trustedrouter/auto" {
				t.Errorf("GetCachedTrustedRouterModels returned %v, want stale cache", got)
			}
		}()
	}
	wg.Wait()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for background refresh request")
	}

	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("background refresh requests = %d, want 1", got)
	}

	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !trustedRouterCacheRefreshInFlight.Load() {
			if refreshed := GetCachedTrustedRouterModels(""); !slices.Equal(refreshed, []string{"trustedrouter/zdr"}) {
				t.Fatalf("refreshed cache = %v, want [trustedrouter/zdr]", refreshed)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("background refresh guard remained in-flight after refresh completed")
}
