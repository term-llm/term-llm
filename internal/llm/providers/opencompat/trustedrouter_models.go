package opencompat

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/term-llm/term-llm/internal/cache"
)

const trustedRouterCacheKey = "trustedrouter"

var trustedRouterCacheRefreshInFlight atomic.Bool

func GetCachedTrustedRouterModels(apiKey string) []string {
	models := GetCachedTrustedRouterModelInfos(apiKey)
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}

func GetCachedTrustedRouterModelInfos(apiKey string) []ModelInfo {
	return getCachedModelInfos(apiKey, "TRUSTEDROUTER_API_KEY", trustedRouterCacheKey, &trustedRouterCacheRefreshInFlight, refreshTrustedRouterCache, fetchTrustedRouterModelInfosSync)
}

func fetchTrustedRouterModelInfosSync(apiKey string) []ModelInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	models, err := NewTrustedRouterProvider(apiKey, "").ListModels(ctx)
	if err != nil || len(models) == 0 {
		return nil
	}
	return models
}

func refreshTrustedRouterCache(apiKey string) {
	_ = fetchTrustedRouterModelInfosSync(apiKey)
}

func RefreshTrustedRouterCacheSync(models []ModelInfo) {
	if len(models) == 0 {
		return
	}
	modelInfos := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		if strings.TrimSpace(m.ID) != "" {
			modelInfos = append(modelInfos, m)
		}
	}
	if len(modelInfos) == 0 {
		return
	}
	_ = cache.WriteModelInfoCache(trustedRouterCacheKey, ModelInfosToCache(modelInfos))
}

func trustedRouterCachedInputLimit(model string) int {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return 0
	}
	cached, err := cache.ReadModelCache(trustedRouterCacheKey)
	if err != nil || cached == nil {
		return 0
	}
	canonical := ""
	if strings.HasPrefix(model, "nyte/") {
		canonical = "trustedrouter/" + strings.TrimPrefix(model, "nyte/")
	}
	for _, m := range cached.ModelInfos {
		id := strings.ToLower(strings.TrimSpace(m.ID))
		if id == model || (canonical != "" && id == canonical) {
			return m.InputLimit
		}
	}
	return 0
}
