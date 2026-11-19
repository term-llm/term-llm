package search

import (
	"strings"

	"github.com/term-llm/term-llm/internal/config"
)

func searchBaseURL(cfg *config.Config, provider string) string {
	switch provider {
	case "exa":
		return cfg.Search.Exa.BaseURL
	case "perplexity":
		return cfg.Search.Perplexity.BaseURL
	case "parallel":
		return cfg.Search.Parallel.BaseURL
	case "tavily":
		return cfg.Search.Tavily.BaseURL
	case "brave":
		return cfg.Search.Brave.BaseURL
	case "google":
		return cfg.Search.Google.BaseURL
	case "keenable":
		return cfg.Search.Keenable.BaseURL
	case "youcom":
		return cfg.Search.YouCom.BaseURL
	case "sofya":
		return cfg.Search.Sofya.BaseURL
	case "duckduckgo":
		return cfg.Search.DuckDuckGo.BaseURL
	}
	return ""
}

// HasBaseURLOverride reports an explicit selected API root without resolving
// it. A missing credential must not silently turn a gateway request into a
// request to another public search provider.
func HasBaseURLOverride(cfg *config.Config) bool {
	provider := cfg.Search.Provider
	if provider == "" {
		provider = config.DefaultSearchProvider
	}
	if provider == "exa_mcp" {
		url := strings.TrimSpace(cfg.Search.ExaMCP.URL)
		return url != "" && url != config.DefaultSearchExaMCPURL
	}
	return strings.TrimSpace(searchBaseURL(cfg, provider)) != ""
}

// NewSearcher resolves only the selected provider's endpoint and credentials.
// base_url includes API version and proxy prefix; fixed request paths append
// to it. Exa MCP retains its existing full-endpoint url contract.
func NewSearcher(cfg *config.Config) (Searcher, error) {
	provider := cfg.Search.Provider
	if provider == "" {
		provider = config.DefaultSearchProvider
	}
	raw := searchBaseURL(cfg, provider)
	base, err := config.ResolveBaseURL("search."+provider+".base_url", raw, "")
	if err != nil {
		return nil, err
	}
	searcher, err := newSearcher(cfg)
	if err != nil {
		return nil, err
	}
	if base == "" {
		return searcher, nil
	}
	switch s := searcher.(type) {
	case *ExaSearcher:
		s.baseURL = base
	case *PerplexitySearcher:
		s.baseURL = base
	case *ParallelSearcher:
		s.baseURL = base
	case *TavilySearcher:
		s.baseURL = base
	case *BraveSearcher:
		s.baseURL = base
	case *GoogleSearcher:
		s.baseURL = base
	case *KeenableSearcher:
		s.baseURL = base
	case *YouComSearcher:
		s.baseURL = base
	case *SofyaSearcher:
		s.baseURL = base
	case *DuckDuckGoLite:
		s.baseURL = base + "/lite/"
	}
	return searcher, nil
}
