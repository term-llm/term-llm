package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/providerhttp"
	"github.com/term-llm/term-llm/internal/proxyclient"
)

// sofyaBaseURL is the Sofya API root; the search operation appends /search.
const sofyaBaseURL = "https://sofya.co/v1"

// SofyaSearcher implements Searcher using the Sofya Search API.
type SofyaSearcher struct {
	baseURL string
	client  *http.Client
	apiKey  string
}

func NewSofyaSearcher(apiKey string, client *http.Client) *SofyaSearcher {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &SofyaSearcher{
		baseURL: sofyaBaseURL,
		client:  client,
		apiKey:  strings.TrimSpace(apiKey),
	}
}

type sofyaRequest struct {
	Query       string `json:"query"`
	SearchDepth string `json:"search_depth"`
	MaxResults  int    `json:"max_results"`
}

type sofyaResponse struct {
	Results []sofyaResult `json:"results"`
}

type sofyaResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

func (s *SofyaSearcher) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("empty query")
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 20 {
		maxResults = 20
	}

	// "snippets" returns result snippets without reading each page; read_url
	// fetches full pages separately.
	jsonBody, err := json.Marshal(sofyaRequest{Query: query, SearchDepth: "snippets", MaxResults: maxResults})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/search", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := proxyclient.Do(s.client, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, providerhttp.NewStatusErrorMessagef(resp, body, "sofya http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var sofyaResp sofyaResponse
	if err := json.Unmarshal(body, &sofyaResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	results := make([]Result, 0, len(sofyaResp.Results))
	for _, r := range sofyaResp.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		snippet := r.Description
		if snippet == "" {
			snippet = r.Content
		}
		results = append(results, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: snippet,
		})
		if len(results) == maxResults {
			break
		}
	}
	return results, nil
}
