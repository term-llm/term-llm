package serve

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/term-llm/term-llm/internal/app/runtime"
	"github.com/term-llm/term-llm/internal/llm"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/providers/opencompat"
)

func (s *serveServer) listModels(ctx context.Context, queryProvider string, fallback bool) ([]core.ModelInfo, string, error) {
	provider, effectiveName, err := s.getModelsProvider(queryProvider)
	if err != nil {
		return nil, "", err
	}

	models := make([]core.ModelInfo, 0)
	// OpenRouter ships hundreds of models and maintains a provider-side warm
	// cache; use it first when available. For other providers, preserve the
	// OpenAI-compatible /v1/models behavior: prefer a fresh/cache upstream
	// ListModels result, and use local curated/configured lists only as a
	// fallback when upstream listing is unavailable or fails.
	pc, hasCfg := s.cfgRef.Providers[effectiveName]
	isOpenRouter := effectiveName == "openrouter" || (hasCfg && string(pc.Type) == "openrouter")
	isTrustedRouter := effectiveName == "trustedrouter" || (hasCfg && string(pc.Type) == "trustedrouter")
	if (isOpenRouter || isTrustedRouter) && (!hasCfg || strings.TrimSpace(pc.BaseURL) == "") {
		apiKey := ""
		if hasCfg {
			apiKey = pc.ResolvedAPIKey
		}
		if isTrustedRouter {
			models = append(models, opencompat.GetCachedTrustedRouterModelInfos(apiKey)...)
		} else {
			models = append(models, opencompat.GetCachedOpenRouterModelInfos(apiKey)...)
		}
	}
	if len(models) == 0 {
		models = s.getCachedModelsForProvider(effectiveName)
	}
	if len(models) == 0 {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if lister, ok := provider.(interface {
			ListModels(context.Context) ([]core.ModelInfo, error)
		}); ok {
			listed, err := lister.ListModels(ctx)
			if err == nil && ctx.Err() == nil {
				s.setCachedModelsForProvider(effectiveName, listed)
				models = listed
			} else if err != nil && !errors.Is(err, llm.ErrListModelsUnsupported) {
				s.verboseLog("ListModels(%q) failed: %v", effectiveName, err)
			}
		}
	}
	if fallback && len(models) == 0 {
		models = llm.LocalDiscoveryModels(s.cfgRef, effectiveName)
	}
	return models, effectiveName, nil
}

const serveModelsCacheTTL = 15 * time.Minute

type serveModelsCacheEntry struct {
	models    []core.ModelInfo
	expiresAt time.Time
}

func (s *serveServer) getCachedModelsForProvider(name string) []core.ModelInfo {
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()

	if entry, ok := s.modelsCache[name]; ok {
		if time.Now().Before(entry.expiresAt) {
			return slices.Clone(entry.models)
		}
		delete(s.modelsCache, name)
	}
	return nil
}

func (s *serveServer) setCachedModelsForProvider(name string, models []core.ModelInfo) {
	if len(models) == 0 {
		return
	}

	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()

	select {
	case <-s.shutdownCh:
		return
	default:
	}
	if s.modelsCache == nil {
		s.modelsCache = make(map[string]serveModelsCacheEntry)
	}
	s.modelsCache[name] = serveModelsCacheEntry{
		models:    slices.Clone(models),
		expiresAt: time.Now().Add(serveModelsCacheTTL),
	}
}

func (s *serveServer) getModelsProvider(name string) (core.Provider, string, error) {
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()
	select {
	case <-s.shutdownCh:
		return nil, "", context.Canceled
	default:
	}
	if name == "" {
		name = s.cfgRef.DefaultProvider
	}
	if p := s.modelsProviders[name]; p != nil {
		return p, name, nil
	}

	// Listing is discovery, not inference. A restricted provider can be
	// constructed with a permitted model even if its default is blocked.
	var model string
	if err := llm.CheckProviderModelAllowed(s.cfgRef, name, ""); err != nil {
		if allowed := llm.ProviderAllowedModelIDs(s.cfgRef, name); len(allowed) != 0 {
			model = allowed[0]
		}
	}
	// Provider construction resolves credentials in-place, including env and
	// header maps. Do not mutate the config being read by composer searches.
	cfg := runtime.CloneConfig(s.cfgRef)
	if pc, ok := cfg.Providers[name]; ok {
		pc.Env, pc.Headers = maps.Clone(pc.Env), maps.Clone(pc.Headers)
		cfg.Providers[name] = pc
	}
	p, err := llm.NewProviderByName(cfg, name, model)
	if err != nil {
		return nil, "", err
	}
	if s.modelsProviders == nil {
		s.modelsProviders = make(map[string]core.Provider)
	}
	s.modelsProviders[name] = p
	return p, name, nil
}

func (s *serveServer) cleanModelProviders() {
	s.modelsMu.Lock()
	for _, p := range s.modelsProviders {
		if cleaner, ok := p.(interface{ CleanupMCP() }); ok {
			cleaner.CleanupMCP()
		}
	}
	s.modelsProviders = nil
	s.modelsCache = nil
	s.modelsMu.Unlock()

}
