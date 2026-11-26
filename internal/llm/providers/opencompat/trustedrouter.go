package opencompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/term-llm/term-llm/internal/config"
)

const trustedRouterBaseURL = "https://api.trustedrouter.com/v1"

// TrustedRouterProvider talks to TrustedRouter's OpenAI-compatible API.
// Its public /v1/models catalog exposes hundreds of upstream chat models and
// routing presets (trustedrouter/*, nyte/*) with context lengths, per-token
// pricing, and filtering metadata.
type TrustedRouterProvider struct {
	*OpenAICompatProvider
}

func NewTrustedRouterProvider(apiKey, model string) *TrustedRouterProvider {
	apiKey = config.NormalizeVeniceAPIKey(apiKey)
	if model == "" {
		model = config.DefaultProviderModel("trustedrouter")
	}
	return &TrustedRouterProvider{
		OpenAICompatProvider: NewOpenAICompatProvider(trustedRouterBaseURL, apiKey, model, "TrustedRouter"),
	}
}

func (p *TrustedRouterProvider) Capabilities() Capabilities {
	return Capabilities{
		NativeWebSearch:    false,
		NativeWebFetch:     false,
		ToolCalls:          true,
		SupportsToolChoice: true,
	}
}

func (p *TrustedRouterProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	resp, err := p.makeRequest(ctx, "GET", "/models", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list TrustedRouter models: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read TrustedRouter models response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, newHTTPStatusError("TrustedRouter", resp, body)
	}

	var catalog trustedRouterModelsResponse
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("failed to parse TrustedRouter models response: %w", err)
	}

	models := make([]ModelInfo, 0, len(catalog.Data))
	seen := make(map[string]bool, len(catalog.Data))
	for _, m := range catalog.Data {
		if !trustedRouterModelIsChat(m) {
			continue
		}
		id := strings.TrimSpace(m.ID)
		if seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, p.trustedRouterModelInfo(m, id))
	}

	if !p.customBaseURL && len(models) > 0 {
		RefreshTrustedRouterCacheSync(models)
	}
	return models, nil
}

func (p *TrustedRouterProvider) trustedRouterModelInfo(m trustedRouterModel, id string) ModelInfo {
	ownedBy := strings.TrimSpace(m.OwnedBy)
	if ownedBy == "" && m.TrustedRouter != nil {
		ownedBy = strings.TrimSpace(m.TrustedRouter.Provider)
	}

	inputLimit := m.ContextLength
	if inputLimit == 0 && m.TopProvider != nil {
		inputLimit = m.TopProvider.ContextLength
	}
	if inputLimit == 0 {
		inputLimit = InputLimitForProviderModel("trustedrouter", id)
	}

	outputLimit := 0
	if m.TopProvider != nil {
		outputLimit = m.TopProvider.MaxCompletionTokens
	}

	inputPrice, outputPrice := -1.0, -1.0
	if m.Pricing != nil {
		inputPrice = roundPerMillionUSD(normalizeCompatModelPrice(m.Pricing.Prompt.Float64()))
		outputPrice = roundPerMillionUSD(normalizeCompatModelPrice(m.Pricing.Completion.Float64()))
	}

	info := ModelInfo{
		ID:          id,
		DisplayName: m.Name,
		Created:     m.Created,
		OwnedBy:     ownedBy,
		InputLimit:  inputLimit,
		OutputLimit: outputLimit,
		InputPrice:  inputPrice,
		OutputPrice: outputPrice,
	}
	if m.TrustedRouter != nil && m.TrustedRouter.Capabilities != nil && len(m.TrustedRouter.Capabilities.ReasoningEffort) > 0 {
		info.ReasoningEfforts = cloneEfforts(m.TrustedRouter.Capabilities.ReasoningEffort)
	}
	if entry, ok := p.modelConfigForName(id); ok {
		if len(entry.ReasoningEfforts) > 0 {
			info.ReasoningEfforts = cloneEfforts(entry.ReasoningEfforts)
		}
		if defaultEffort := strings.TrimSpace(entry.DefaultReasoningEffort); defaultEffort != "" {
			info.DefaultReasoningEffort = defaultEffort
		}
	}
	return info
}

type trustedRouterModelsResponse struct {
	Data []trustedRouterModel `json:"data"`
}

type trustedRouterModel struct {
	ID            string                      `json:"id"`
	Name          string                      `json:"name,omitempty"`
	Created       int64                       `json:"created,omitempty"`
	OwnedBy       string                      `json:"owned_by,omitempty"`
	ContextLength int                         `json:"context_length,omitempty"`
	Architecture  *trustedRouterArchitecture  `json:"architecture,omitempty"`
	Pricing       *oaiModelPricing            `json:"pricing,omitempty"`
	TopProvider   *trustedRouterTopProvider   `json:"top_provider,omitempty"`
	TrustedRouter *trustedRouterModelMetadata `json:"trustedrouter,omitempty"`
}

type trustedRouterArchitecture struct {
	Modality         string   `json:"modality,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
}

type trustedRouterTopProvider struct {
	ContextLength       int `json:"context_length,omitempty"`
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
}

type trustedRouterModelMetadata struct {
	Provider            string                     `json:"provider,omitempty"`
	InternalOnly        bool                       `json:"internal_only,omitempty"`
	ConfigurationHidden bool                       `json:"configuration_hidden,omitempty"`
	SyntheticMonitor    bool                       `json:"synthetic_monitor,omitempty"`
	SupportsChat        *bool                      `json:"supports_chat,omitempty"`
	SupportsEmbeddings  bool                       `json:"supports_embeddings,omitempty"`
	SupportsRerank      bool                       `json:"supports_rerank,omitempty"`
	SupportsVideo       bool                       `json:"supports_video,omitempty"`
	SupportsDecide      bool                       `json:"supports_decide,omitempty"`
	Aliases             []string                   `json:"aliases,omitempty"`
	Capabilities        *trustedRouterCapabilities `json:"capabilities,omitempty"`
}

type trustedRouterCapabilities struct {
	ReasoningEffort []string `json:"reasoning_effort,omitempty"`
}

func trustedRouterModelIsChat(m trustedRouterModel) bool {
	if strings.TrimSpace(m.ID) == "" {
		return false
	}
	if tr := m.TrustedRouter; tr != nil {
		if tr.InternalOnly || tr.ConfigurationHidden || tr.SyntheticMonitor {
			return false
		}
		if tr.SupportsChat != nil {
			return *tr.SupportsChat
		}
		if tr.SupportsEmbeddings || tr.SupportsRerank || tr.SupportsVideo || tr.SupportsDecide {
			return false
		}
	}
	return trustedRouterArchitectureIsChat(m.Architecture)
}

func trustedRouterArchitectureIsChat(arch *trustedRouterArchitecture) bool {
	if arch == nil {
		return true
	}
	if len(arch.OutputModalities) > 0 {
		output := lowerStringSet(arch.OutputModalities)
		if !output["text"] {
			return false
		}
		for _, nonChat := range []string{"embedding", "image", "video", "audio", "speech", "scores", "decision", "transcription"} {
			if output[nonChat] {
				return false
			}
		}
		return true
	}
	modality := strings.ToLower(strings.TrimSpace(arch.Modality))
	if _, rhs, ok := strings.Cut(modality, "->"); ok {
		if !strings.Contains(rhs, "text") {
			return false
		}
		for _, nonChat := range []string{"image", "video", "audio", "speech", "embedding", "scores", "decision", "transcription"} {
			if strings.Contains(rhs, nonChat) {
				return false
			}
		}
	}
	return true
}

func roundPerMillionUSD(v float64) float64 {
	return math.Round(v*1_000_000) / 1_000_000
}
