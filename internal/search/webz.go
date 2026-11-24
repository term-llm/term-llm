package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/term-llm/term-llm/internal/providerhttp"
)

// webzBaseURL is the News Search API root. The search operation appends
// /api/news/context. Coverage is the API's last 30 days; the request does not
// send a days field.
const (
	webzBaseURL       = "https://api.webz.io"
	webzSearchPath    = "/api/news/context"
	webzMaxQueryChars = 750
	webzMaxQueryWords = 100
)

// WebzSearcher implements Searcher using the Webz.io News Search API.
type WebzSearcher struct {
	baseURL string
	client  *http.Client
	apiKey  string
}

func NewWebzSearcher(apiKey string, client *http.Client) *WebzSearcher {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &WebzSearcher{
		baseURL: webzBaseURL,
		client:  client,
		apiKey:  strings.TrimSpace(apiKey),
	}
}

type webzRequest struct {
	Query string `json:"query"`
	K     int    `json:"k"`
}

type webzResponse struct {
	Results []webzResult `json:"results"`
}

type webzResult struct {
	Article webzArticle `json:"article"`
	Chunk   webzChunk   `json:"chunk"`
}

type webzArticle struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Summary string `json:"summary"`
}

type webzChunk struct {
	Text string `json:"text"`
}

func (w *WebzSearcher) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}
	if n := utf8.RuneCountInString(query); n > webzMaxQueryChars {
		return nil, fmt.Errorf("query is too long: %d characters (max %d)", n, webzMaxQueryChars)
	}
	if n := len(strings.Fields(query)); n > webzMaxQueryWords {
		return nil, fmt.Errorf("query is too long: %d words (max %d)", n, webzMaxQueryWords)
	}
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 20 {
		maxResults = 20
	}

	jsonBody, err := json.Marshal(webzRequest{Query: query, K: maxResults})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.baseURL+webzSearchPath, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.apiKey)

	resp, err := doSearchRequest(w.client, req, w.apiKey != "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := readSearchResponse(resp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, providerhttp.NewStatusErrorMessagef(resp, body, "webz http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var webzResp webzResponse
	if err := json.Unmarshal(body, &webzResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	results := make([]Result, 0, len(webzResp.Results))
	for _, r := range webzResp.Results {
		if strings.TrimSpace(r.Article.URL) == "" {
			continue
		}
		snippet := strings.TrimSpace(r.Chunk.Text)
		if snippet == "" {
			snippet = strings.TrimSpace(r.Article.Summary)
		}
		results = append(results, Result{
			Title:   r.Article.Title,
			URL:     r.Article.URL,
			Snippet: snippet,
		})
		if len(results) == maxResults {
			break
		}
	}
	return results, nil
}
