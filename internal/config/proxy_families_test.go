package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/term-llm/term-llm/internal/proxyclient"
)

const familyProxyToken = "test-token-for-proxy-families"

func familyProvider(id, family string, routes ...proxyDiscoveredRoute) proxyDiscoveredProvider {
	return proxyDiscoveredProvider{ID: id, Family: family, Routes: routes}
}

func familyRoute(name, profile, mount string) proxyDiscoveredRoute {
	return proxyDiscoveredRoute{Name: name, Profile: profile, LocalPrefix: mount}
}

func loadFamilyProxy(t *testing.T, payload proxyDiscoveryPayload, proxyExtra, local string) (*Config, string) {
	t.Helper()
	resetProxyDiscoveryCache()
	t.Cleanup(resetProxyDiscoveryCache)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_proxy/client/v2/providers" {
			if r.Header.Get("Authorization") != "Bearer "+familyProxyToken {
				t.Error("discovery request lacked its caller token")
			}
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_proxy/client/unavailable/") {
			if r.Header.Get("x-api-key") != familyProxyToken {
				t.Error("blocked Exa request lacked its caller token")
			}
			w.WriteHeader(http.StatusForbidden)
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	writeTestConfig(t, home, "proxy:\n  url: "+srv.URL+"\n  token: "+familyProxyToken+"\n"+proxyExtra+local)
	cfg, err := LoadWithoutCredentials()
	if err != nil {
		t.Fatal(err)
	}
	return cfg, srv.URL
}

func TestProxyExaParentBindsBothProfilesAndFailsClosed(t *testing.T) {
	rest := familyRoute("rest", "exa", "/search-rest/")
	mcp := familyRoute("mcp", "exa-mcp", "/search-mcp/")
	for _, tc := range []struct {
		name              string
		routes            []proxyDiscoveredRoute
		wantREST, wantMCP string
	}{
		{"both", []proxyDiscoveredRoute{rest, mcp}, "/search-rest", "/search-mcp/mcp"},
		{"mcp-only", []proxyDiscoveredRoute{mcp}, "/_proxy/client/unavailable/exa", "/search-mcp/mcp"},
		{"neither", nil, "/_proxy/client/unavailable/exa", "/_proxy/client/unavailable/exa-mcp/mcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := proxyDiscoveryPayload{}
			if len(tc.routes) > 0 {
				payload.Providers = []proxyDiscoveredProvider{familyProvider("exa", "search", tc.routes...)}
			}
			cfg, root := loadFamilyProxy(t, payload, "  providers: [exa]\n", "")
			if cfg.Search.Exa.BaseURL != root+tc.wantREST || cfg.Search.ExaMCP.URL != root+tc.wantMCP || cfg.Search.Exa.APIKey != familyProxyToken || cfg.Search.ExaMCP.APIKey != familyProxyToken {
				t.Fatal("wrong per-profile Exa bindings")
			}
			if _, ok := cfg.Providers["exa"]; ok {
				t.Fatal("search provider was seeded as an inference provider")
			}
			if tc.name != "neither" {
				return
			}
			for _, endpoint := range []string{cfg.Search.Exa.BaseURL + "/search", cfg.Search.ExaMCP.URL} {
				req, err := http.NewRequestWithContext(context.Background(), "POST", endpoint, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("x-api-key", cfg.Search.Exa.APIKey)
				resp, err := proxyclient.Do(nil, req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("status = %d", resp.StatusCode)
				}
			}
		})
	}
}

func TestProxyAllKeylessFallbackSuppressesLocalCredentials(t *testing.T) {
	t.Setenv("EXA_API_KEY", "env-exa")
	t.Setenv("YDC_API_KEY", "env-youcom")
	t.Setenv("KEENABLE_API_KEY", "env-keenable")
	for _, selection := range []string{"", "  direct: [exa]\n"} {
		cfg, root := loadFamilyProxy(t, proxyDiscoveryPayload{}, selection, "")
		if cfg.Search.ExaMCP.URL != DefaultSearchExaMCPURL || cfg.Search.Jina.BaseURL != "" || cfg.Search.YouCom.BaseURL != "" || cfg.Search.Keenable.BaseURL != "" {
			t.Fatal("unadvertised keyless profile was proxied")
		}
		wantExa := ""
		if selection != "" {
			wantExa = "env-exa"
			if cfg.Search.Exa.BaseURL != "" {
				t.Fatal("parent direct exception ignored")
			}
		} else if cfg.Search.Exa.BaseURL != unavailableProxyEndpoint(root, "exa") {
			t.Fatal("keyed Exa REST was not bound fail-closed")
		}
		if got, err := cfg.Search.ExaMCPKey(cfg.Search.ExaMCP.URL).Resolve(); got != wantExa || err != nil {
			t.Fatalf("Exa MCP key: want present = %v; err=%v", wantExa != "", err)
		}
		for name, ref := range map[string]CredentialRef{"You.com": cfg.Search.YouComKey(), "Keenable": cfg.Search.KeenableKey()} {
			if got, err := ref.Resolve(); got != "" || err != nil {
				t.Errorf("%s sent local key or failed: %v", name, err)
			}
		}
		cfg.Search.ExaMCP.APIKey, cfg.Search.YouCom.APIKey, cfg.Search.Keenable.APIKey = "$(must-not-run)", "$(must-not-run)", "$(must-not-run)"
		if selection == "" && (cfg.Search.ExaMCPKey(cfg.Search.ExaMCP.URL).Configured() || cfg.Search.YouComKey().Configured() || cfg.Search.KeenableKey().Configured()) {
			t.Fatal("key suppression did not cover explicit credentials")
		}
	}
}

func TestProxySofyaUsesScopedRouteOrStaysUnavailable(t *testing.T) {
	t.Setenv("SOFYA_API_KEY", "local-env-key")
	const local = "search:\n  provider: sofya\n  sofya:\n    base_url: https://local.example.test/v1\n"
	const configuredKey = "    api_key: local-config-key\n"
	granted := proxyDiscoveryPayload{Providers: []proxyDiscoveredProvider{
		familyProvider("sofya", "search", familyRoute("sofya-route", "sofya", "/sofya-granted/")),
	}}
	for _, tc := range []struct {
		name, selection, extra, path, key string
		payload                           proxyDiscoveryPayload
	}{
		{name: "proxy-all missing route with env key", path: "/_proxy/client/unavailable/sofya", key: familyProxyToken},
		{name: "proxy-all missing route with configured key", extra: configuredKey, path: "/_proxy/client/unavailable/sofya", key: familyProxyToken},
		{name: "allowlist missing route", selection: "  providers: [sofya]\n", path: "/_proxy/client/unavailable/sofya", key: familyProxyToken},
		{name: "granted route", extra: configuredKey, path: "/sofya-granted", key: familyProxyToken, payload: granted},
		{name: "direct opt-out", selection: "  direct: [sofya]\n", key: "local-env-key", payload: granted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, root := loadFamilyProxy(t, tc.payload, tc.selection, local+tc.extra)
			wantRoot := "https://local.example.test/v1"
			if tc.path != "" {
				wantRoot = root + tc.path
			}
			if cfg.Search.Provider != "sofya" || cfg.Search.Sofya.BaseURL != wantRoot {
				t.Fatalf("wrong Sofya selector or root: %q, %q", cfg.Search.Provider, cfg.Search.Sofya.BaseURL)
			}
			if key, err := cfg.Search.SofyaKey().Resolve(); err != nil || key != tc.key {
				t.Fatalf("wrong Sofya credential (err=%v)", err)
			}
			if binding, ok := proxyclient.Lookup(wantRoot + "/search"); ok != (tc.path != "") || ok && (binding.Root != wantRoot || binding.Token != familyProxyToken) {
				t.Fatal("wrong Sofya transport binding")
			}
		})
	}
}

func TestProxyWebzUsesScopedRouteOrStaysUnavailable(t *testing.T) {
	t.Setenv("WEBZ_API_TOKEN", "local-env-key")
	const local = "search:\n  provider: webz\n  webz:\n    base_url: https://local.example.test\n"
	const configuredKey = "    api_key: local-config-key\n"
	granted := proxyDiscoveryPayload{Providers: []proxyDiscoveredProvider{
		familyProvider("webz", "search", familyRoute("webz-route", "webz", "/webz-granted/")),
	}}
	for _, tc := range []struct {
		name, selection, extra, path, key string
		payload                           proxyDiscoveryPayload
	}{
		{name: "proxy-all missing route with env key", path: "/_proxy/client/unavailable/webz", key: familyProxyToken},
		{name: "proxy-all missing route with configured key", extra: configuredKey, path: "/_proxy/client/unavailable/webz", key: familyProxyToken},
		{name: "allowlist missing route", selection: "  providers: [webz]\n", path: "/_proxy/client/unavailable/webz", key: familyProxyToken},
		{name: "granted route", extra: configuredKey, path: "/webz-granted", key: familyProxyToken, payload: granted},
		{name: "direct opt-out", selection: "  direct: [webz]\n", key: "local-env-key", payload: granted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, root := loadFamilyProxy(t, tc.payload, tc.selection, local+tc.extra)
			wantRoot := "https://local.example.test"
			if tc.path != "" {
				wantRoot = root + tc.path
			}
			if cfg.Search.Provider != "webz" || cfg.Search.Webz.BaseURL != wantRoot {
				t.Fatalf("wrong Webz selector or root: %q, %q", cfg.Search.Provider, cfg.Search.Webz.BaseURL)
			}
			if key, err := cfg.Search.WebzKey().Resolve(); err != nil || key != tc.key {
				t.Fatalf("wrong Webz credential (err=%v)", err)
			}
			if binding, ok := proxyclient.Lookup(wantRoot + "/api/news/context"); ok != (tc.path != "") || ok && (binding.Root != wantRoot || binding.Token != familyProxyToken) {
				t.Fatal("wrong Webz transport binding")
			}
		})
	}
}

func TestProxyGrokCompanionBindsUnderOwnBuiltin(t *testing.T) {
	payload := proxyDiscoveryPayload{Providers: []proxyDiscoveredProvider{familyProvider("grok", "inference",
		familyRoute("grok", "grok", "/groky/"), familyRoute("grok-bin", "grok-bin", "/grokcli/"))}}
	for _, name := range []string{"grok", "grok-bin"} {
		cfg, root := loadFamilyProxy(t, payload, "  providers: ["+name+"]\n", "")
		if cfg.Providers["grok-bin"].BaseURL != root+"/grokcli" {
			t.Fatal("Grok bin route not bound independently")
		}
		if got := cfg.Providers["grok"].BaseURL; (name == "grok" && got != root+"/groky") || (name == "grok-bin" && got != "") {
			t.Fatalf("Grok root = %q when %s selected", got, name)
		}
	}
}

func TestProxyInferenceRouteAndAncillaryOwnership(t *testing.T) {
	payload := proxyDiscoveryPayload{Providers: []proxyDiscoveredProvider{
		familyProvider("chatgpt", "inference", familyRoute("dictation", "chatgpt-backend", "/wham/")),
		familyProvider("openai-work", "inference", familyRoute("production", "openai", "/named-openai/")),
		familyProvider("mistral", "inference", familyRoute("mistral", "mistral", "/mistral-inference/")),
	}}
	cfg, root := loadFamilyProxy(t, payload, "  providers: [chatgpt, openai-work, mistral]\n", "image:\n  openai:\n    base_url: https://local.example.test/v1\n")
	if cfg.Providers["chatgpt"].BaseURL != unavailableProxyEndpoint(root, "chatgpt") || cfg.Transcription.ChatGPT.BaseURL != root+"/wham" {
		t.Fatal("companion advertised without primary was used for inference")
	}
	if cfg.Providers["openai-work"].BaseURL != root+"/named-openai" || cfg.Providers["openai"].BaseURL != "" || cfg.Image.OpenAI.BaseURL != "https://local.example.test/v1" {
		t.Fatal("named inference route unexpectedly claimed canonical settings")
	}
	if cfg.Providers["mistral"].BaseURL != root+"/mistral-inference" {
		t.Fatal("Mistral route was not recognized as inference-capable")
	}
}

func TestProxySlotResolutionUsesOnlyServedRoutes(t *testing.T) {
	exa := familyProvider("exa", "search", familyRoute("exa-rest", "exa", "/rest/"), familyRoute("exa-stream", "exa-mcp", "/mcp/"))
	brave := familyProvider("brave", "search", familyRoute("brave-route", "brave", "/brave/"))
	flux := familyProvider("flux", "media", familyRoute("flux", "flux", "/flux/"))
	inference := []proxyDiscoveredProvider{
		familyProvider("openai", "inference", familyRoute("openai", "openai", "/openai/")),
		familyProvider("gemini", "inference", familyRoute("gemini", "gemini", "/gemini/")),
	}
	defaults := map[string]proxyDiscoveredDefault{"search": {Provider: "exa", Route: "exa-rest"}, "fetch": {Provider: "exa", Route: "exa-stream"}}
	for _, tc := range []struct {
		name, selection, local string
		providers              []proxyDiscoveredProvider
		defaults               map[string]proxyDiscoveredDefault
		search, fetch          string
	}{
		{"operator", "  providers: [exa, brave]\n", "", []proxyDiscoveredProvider{exa, brave}, defaults, "exa", "exa_mcp"},
		{"explicit", "  providers: [exa]\n", "search:\n  provider: exa_mcp\n  fetch_provider: jina\n", []proxyDiscoveredProvider{exa}, defaults, "exa_mcp", "jina"},
		{"filtered-default", "  providers: [brave]\n", "", []proxyDiscoveredProvider{exa, brave}, defaults, "brave", "jina"},
		{"builtin-served", "  providers: [exa]\n", "", []proxyDiscoveredProvider{exa}, nil, "exa_mcp", "exa_mcp"},
		{"unavailable-not-served", "  providers: [exa, brave, flux]\n", "", []proxyDiscoveredProvider{brave, flux}, nil, "brave", "jina"},
		{"multi-inference-proxy-all", "", "", inference, nil, "exa_mcp", "jina"},
		{"ambiguous-search", "", "", []proxyDiscoveredProvider{brave, familyProvider("exa", "search", exa.Routes[0])}, nil, "exa_mcp", "jina"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := loadFamilyProxy(t, proxyDiscoveryPayload{Providers: tc.providers, Defaults: tc.defaults}, tc.selection, tc.local)
			if cfg.Search.Provider != tc.search || cfg.Search.FetchProvider != tc.fetch || cfg.Image.Provider != DefaultImageProvider || cfg.Embed.Provider != "" {
				t.Fatalf("selectors = search %q, fetch %q, image %q, embed %q", cfg.Search.Provider, cfg.Search.FetchProvider, cfg.Image.Provider, cfg.Embed.Provider)
			}
		})
	}
}

func TestNamedInferenceProviderCannotClaimAProfileWithTheSameName(t *testing.T) {
	payload := proxyDiscoveryPayload{Providers: []proxyDiscoveredProvider{
		familyProvider("gemini-live", "inference", familyRoute("gateway", "openai-compatible", "/gateway/")),
	}}
	cfg, root := loadFamilyProxy(t, payload, "  providers: [gemini-live]\n", "live:\n  gemini:\n    base_url: wss://local.example.test/v1\n")
	if cfg.Providers["gemini-live"].BaseURL != root+"/gateway" || cfg.Live.Gemini.BaseURL != "wss://local.example.test/v1" {
		t.Fatal("a named inference route changed an unrelated profile's global keys")
	}
}

func TestNamedChatGPTCompanionIsScopedToItsInferenceRoot(t *testing.T) {
	payload := proxyDiscoveryPayload{Providers: []proxyDiscoveredProvider{
		familyProvider("chatgpt-work", "inference",
			familyRoute("codex-work", "chatgpt", "/inference/work/"),
			familyRoute("dictation-work", "chatgpt-backend", "/account/work/")),
	}}
	cfg, root := loadFamilyProxy(t, payload, "  providers: [chatgpt-work]\n", "")
	pc := cfg.Providers["chatgpt-work"]
	b, ok := proxyclient.Related(pc.BaseURL, "chatgpt-backend")
	if !ok || b.Root != root+"/account/work" || b.Kind != "chatgpt" || b.Token != familyProxyToken {
		t.Fatal("named provider did not register its granted companion")
	}
	if cfg.Transcription.ChatGPT.BaseURL != "" || cfg.Providers["chatgpt"].BaseURL != "" {
		t.Fatal("named provider bound global ChatGPT settings")
	}
}
