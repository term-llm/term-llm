package proxyprofile

import (
	"strings"

	"github.com/term-llm/term-llm/internal/providerspec"
)

// ClassificationDiscoveryHeader negotiates classification support on v2 discovery.
const ClassificationDiscoveryHeader = "X-Term-LLM-Proxy-Capabilities"

// ClassificationDiscoveryCapability opts in to the classification provider family.
const ClassificationDiscoveryCapability = "classification"

// ClassificationDefaultCapability allows the classify slot in discovery defaults.
const ClassificationDefaultCapability = "classification-default"

// Binding names a configuration API root and its corresponding credential.
// An empty Key means the integration uses a delegated account or no vendor key.
type Binding struct{ Root, Key string }

// Profile is public routing metadata, never a credential or a promise of live vendor certification.
type Profile struct {
	ID, Label, Upstream, Header, Prefix, Account, Billing string
	Family                                                string            // inference, search, media, embeddings, or classification
	Parent                                                string            // Primary profile ID for a companion.
	Slots                                                 map[string]string // Feature slot to client selector.
	Allow                                                 []string          // Method and path entries for scoped routes.
	ClassifyProvider                                      string            // Built-in classifier selector; bound by the typed client binder.
	Provider                                              providerspec.Type
	WebSocket                                             bool
	Bindings                                              []Binding
	Gate                                                  string
	PathSuffix                                            string // Full-endpoint clients append this below their route.
	// CLIRootEnv names the provider Env entry that must carry this route's root to
	// a local CLI adapter. The adapter only leaves local-credential mode when it
	// is present, so configuration must set it whenever it binds the provider.
	CLIRootEnv string
}

// All returns a copy of the routing decisions. Each registered provider has a decision,
// including providers whose credentialless CLI transport still requires verification.
func All() []Profile {
	return []Profile{
		{ID: "anthropic", Family: "inference", Label: "Anthropic", Provider: providerspec.TypeAnthropic, Upstream: "https://api.anthropic.com", Header: "x-api-key", Billing: "pay_as_you_go"},
		{
			ID: "openai", Family: "inference", Label: "OpenAI", Provider: providerspec.TypeOpenAI, Upstream: "https://api.openai.com/v1", Header: "Authorization", Prefix: "Bearer ", WebSocket: true, Billing: "pay_as_you_go",
			Slots:    map[string]string{"image": "openai", "embed": "openai", "transcription": "openai"},
			Bindings: []Binding{{"image.openai.base_url", "image.openai.api_key"}, {"embed.openai.base_url", "embed.openai.api_key"}, {"live.openai.base_url", "live.openai.api_key"}},
		},
		{
			ID: "chatgpt", Family: "inference", Label: "ChatGPT", Provider: providerspec.TypeChatGPT, Upstream: "https://chatgpt.com/backend-api/codex", Header: "Authorization", Prefix: "Bearer ", Account: "chatgpt", WebSocket: true, Billing: "plan",
			Slots:    map[string]string{"image": "chatgpt"},
			Bindings: []Binding{{"image.chatgpt.base_url", ""}, {"live.chatgpt.call_base_url", ""}},
		},
		{
			ID: "chatgpt-backend", Family: "inference", Parent: "chatgpt", Label: "ChatGPT Usage & Dictation", Upstream: "https://chatgpt.com/backend-api", Header: "Authorization", Prefix: "Bearer ", Account: "chatgpt", Billing: "plan",
			Slots:    map[string]string{"transcription": "chatgpt"},
			Bindings: []Binding{{"transcription.chatgpt.base_url", ""}},
		},
		{
			ID: "chatgpt-openai", Family: "inference", Parent: "chatgpt", Label: "ChatGPT Embeddings & Live", Upstream: "https://api.openai.com/v1", Header: "Authorization", Prefix: "Bearer ", Account: "chatgpt", Billing: "unspecified", WebSocket: true,
			Slots:    map[string]string{"embed": "chatgpt"},
			Bindings: []Binding{{"embed.chatgpt.base_url", ""}, {"live.chatgpt.sideband_base_url", ""}},
		},
		{ID: "grok", Family: "inference", Label: "Grok", Provider: providerspec.TypeGrok, Upstream: "https://cli-chat-proxy.grok.com/v1", Header: "Authorization", Prefix: "Bearer ", Account: "grok", Billing: "plan"},
		{ID: "copilot", Family: "inference", Label: "GitHub Copilot", Provider: providerspec.TypeCopilot, Upstream: "https://api.githubcopilot.com", Header: "Authorization", Prefix: "Bearer ", Account: "copilot", Billing: "plan"},
		{
			ID: "gemini", Family: "inference", Label: "Gemini", Provider: providerspec.TypeGemini, Upstream: "https://generativelanguage.googleapis.com/v1beta", Header: "x-goog-api-key", Billing: "pay_as_you_go",
			Slots:    map[string]string{"image": "gemini", "audio": "gemini", "embed": "gemini"},
			Bindings: []Binding{{"image.gemini.base_url", "image.gemini.api_key"}, {"audio.gemini.base_url", "audio.gemini.api_key"}, {"embed.gemini.base_url", "embed.gemini.api_key"}},
		},
		{ID: "gemini-live", Family: "inference", Parent: "gemini", Label: "Gemini Live", Upstream: "https://generativelanguage.googleapis.com", Header: "x-goog-api-key", Billing: "pay_as_you_go", WebSocket: true, PathSuffix: "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent", Bindings: []Binding{{"live.gemini.base_url", "live.gemini.api_key"}}},
		{
			ID: "openrouter", Family: "inference", Label: "OpenRouter", Provider: providerspec.TypeOpenRouter, Upstream: "https://openrouter.ai/api/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots:    map[string]string{"image": "openrouter"},
			Bindings: []Binding{{"image.openrouter.base_url", "image.openrouter.api_key"}},
		},
		{
			ID: "xai", Family: "inference", Label: "xAI", Provider: providerspec.TypeXAI, Upstream: "https://api.x.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots:    map[string]string{"image": "xai"},
			Bindings: []Binding{{"image.xai.base_url", "image.xai.api_key"}},
		},
		{
			ID: "venice", Family: "inference", Label: "Venice", Provider: providerspec.TypeVenice, Upstream: "https://api.venice.ai/api/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots:    map[string]string{"image": "venice", "audio": "venice", "music": "venice", "transcription": "venice"},
			Bindings: []Binding{{"image.venice.base_url", "image.venice.api_key"}, {"audio.venice.base_url", "audio.venice.api_key"}, {"music.venice.base_url", "music.venice.api_key"}, {"transcription.venice.base_url", "transcription.venice.api_key"}},
		},
		{ID: "nearai", Family: "inference", Label: "NEAR AI", Provider: providerspec.TypeNearAI, Upstream: "https://cloud-api.near.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go"},
		{ID: "sambanova", Family: "inference", Label: "SambaNova", Provider: providerspec.TypeSambaNova, Upstream: "https://api.sambanova.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go"},
		{ID: "obscurify", Family: "inference", Label: "Obscurify", Provider: providerspec.TypeObscurify, Upstream: "https://obscurify.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go"},
		{ID: "zen", Family: "inference", Label: "OpenCode Zen", Provider: providerspec.TypeZen, Upstream: "https://opencode.ai/zen/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go"},
		{ID: "opencode-go", Family: "inference", Label: "OpenCode Go", Provider: providerspec.TypeOpenCodeGo, Upstream: "https://opencode.ai/zen/go/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "plan"},
		{ID: "opencode-go-messages", Family: "inference", Label: "OpenCode Go · Messages", Upstream: "https://opencode.ai/zen/go", Header: "x-api-key", Billing: "plan"},
		{ID: "opencode-go-gemini", Family: "inference", Label: "OpenCode Go · Gemini", Upstream: "https://opencode.ai/inference/go/v1", Header: "x-goog-api-key", Billing: "plan"},
		{ID: "bedrock", Family: "inference", Label: "Bedrock", Provider: providerspec.TypeBedrock, Header: "Authorization", Prefix: "Bearer ", Account: "aws-sigv4", Billing: "pay_as_you_go"},
		{
			ID: "ollama", Family: "inference", Label: "Ollama", Provider: providerspec.TypeOllama, Upstream: "http://127.0.0.1:11434", Header: "Authorization", Prefix: "Bearer ", Billing: "local",
			Slots:    map[string]string{"embed": "ollama"},
			Bindings: []Binding{{"embed.ollama.base_url", ""}},
		},
		{ID: "vllm", Family: "inference", Label: "vLLM", Provider: providerspec.TypeVLLM, Header: "Authorization", Prefix: "Bearer ", Billing: "unspecified"},
		{ID: "openai-compatible", Family: "inference", Label: "OpenAI-compatible", Provider: providerspec.TypeOpenAICompat, Header: "Authorization", Prefix: "Bearer ", Billing: "unspecified"},
		{ID: "claude-bin", Family: "inference", Label: "Claude Code", Provider: providerspec.TypeClaudeBin, Upstream: "https://api.anthropic.com", Header: "Authorization", Prefix: "Bearer ", Account: "claude-bin", Billing: "plan", CLIRootEnv: "TERM_LLM_CLAUDE_PROXY_ROOT"},
		// Cursor exchanges its API key for an access/refresh pair. The proxy must own
		// that exchange and never pass the vendor token response through to workers.
		{ID: "cursor-bin", Family: "inference", Label: "Cursor", Provider: providerspec.TypeCursorBin, Upstream: "https://api2.cursor.sh", Header: "Authorization", Prefix: "Bearer ", Account: "cursor", Billing: "unspecified", CLIRootEnv: "TERM_LLM_CURSOR_PROXY_ROOT", Gate: "the CLI exchanges its API key at /auth/exchange_user_api_key for vendor access and refresh tokens; shared access requires server-side token custody, verified agent routing, and isolated tool-bridge verification"},
		{ID: "cursor-bin-agent", Family: "inference", Label: "Cursor Agent", Header: "Authorization", Prefix: "Bearer ", Account: "cursor", Billing: "unspecified", Gate: "Cursor agent origin is server-advertised and has not been established"},
		{ID: "cursor-bin-repo", Family: "inference", Label: "Cursor Repository", Upstream: "https://repo42.cursor.sh", Header: "Authorization", Prefix: "Bearer ", Account: "cursor", Billing: "unspecified", Gate: "requires the verified Cursor backend and agent routes"},
		// grok-bin is ungated on fixture evidence only: the real grok 1.0.77 executable completed model listing and an
		// ACP turn with a local MCP tool round trip against a synthetic gateway with all external connections refused.
		// Vendor acceptance of Grok plan credentials for this CLI's API-key wire shape has NOT been tested.
		{ID: "grok-bin", Family: "inference", Parent: "grok", Label: "Grok CLI", Provider: providerspec.TypeGrokBin, Upstream: "https://cli-chat-proxy.grok.com/v1", Header: "Authorization", Prefix: "Bearer ", Account: "grok", Billing: "plan", CLIRootEnv: "TERM_LLM_GROK_CLI_PROXY_ROOT"},
		// The real Linux agy 1.5.12 executable completed isolated synthetic OAuth and an ACP turn.
		// Live subscription acceptance and macOS credential isolation have not been verified.
		{ID: "agy-bin", Family: "inference", Label: "Antigravity", Provider: providerspec.TypeAgyBin, Upstream: "https://daily-cloudcode-pa.googleapis.com", Header: "Authorization", Prefix: "Bearer ", Account: "agy-bin", Billing: "plan", CLIRootEnv: "TERM_LLM_AGY_PROXY_ROOT"},
		{ID: "vibe-bin", Family: "inference", Label: "Mistral Vibe", Provider: providerspec.TypeVibeBin, Upstream: "https://api.mistral.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "unspecified", CLIRootEnv: "TERM_LLM_VIBE_PROXY_ROOT", Gate: "the real vibe-acp 3.1.2 reached only the scoped generic API-key route in a synthetic run and refused a built-in tool call, but it advertises its built-in file and shell tools to the model whatever enabled_tools or disabled_tools say, and bridged MCP tools are reachable only through its built-in run_typescript, which the strict bridge-only policy denies; subscription custody is vendor-dependent"},
		{
			ID: "elevenlabs", Family: "media", Label: "ElevenLabs", Upstream: "https://api.elevenlabs.io", Header: "xi-api-key", Billing: "pay_as_you_go",
			Slots:    map[string]string{"audio": "elevenlabs", "music": "elevenlabs", "transcription": "elevenlabs"},
			Bindings: []Binding{{"audio.elevenlabs.base_url", "audio.elevenlabs.api_key"}, {"music.elevenlabs.base_url", "music.elevenlabs.api_key"}, {"transcription.elevenlabs.base_url", "transcription.elevenlabs.api_key"}},
		},
		{
			ID: "flux", Family: "media", Label: "Flux", Upstream: "https://api.bfl.ai/v1", Header: "x-key", Billing: "pay_as_you_go",
			Slots:    map[string]string{"image": "flux"},
			Bindings: []Binding{{"image.flux.base_url", "image.flux.api_key"}},
		},
		{
			ID: "jina", Family: "embeddings", Label: "Jina", Upstream: "https://api.jina.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots:    map[string]string{"embed": "jina"},
			Bindings: []Binding{{"embed.jina.base_url", "embed.jina.api_key"}},
		},
		{
			ID: "jina-reader", Family: "embeddings", Parent: "jina", Label: "Jina Reader", Upstream: "https://r.jina.ai",
			Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots: map[string]string{"fetch": "jina"}, Allow: []string{"GET /*"},
			Bindings: []Binding{{"search.jina.base_url", ""}},
		},
		{
			ID: "voyage", Family: "embeddings", Label: "Voyage", Upstream: "https://api.voyageai.com/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots:    map[string]string{"embed": "voyage"},
			Bindings: []Binding{{"embed.voyage.base_url", "embed.voyage.api_key"}},
		},
		{
			ID: "typesafe", Family: "classification", Label: "TypeSafe", ClassifyProvider: "typesafe",
			Slots:    map[string]string{"classify": "typesafe"},
			Upstream: "https://api.typesafe.ai", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Allow: []string{"POST =/v1/systemone", "GET =/v1/models"},
		},
		{
			ID: "openai-decisions", Family: "classification", Label: "OpenAI Decisions", ClassifyProvider: "openai",
			Slots:    map[string]string{"classify": "openai"},
			Upstream: "https://api.openai.com/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "unspecified",
			Allow: []string{"POST =/decisions"},
		},
		{
			ID: "cloudflare-clef", Family: "classification", Label: "Cloudflare Clef", ClassifyProvider: "cloudflare",
			Slots:  map[string]string{"classify": "cloudflare"},
			Header: "Authorization", Prefix: "Bearer ", Billing: "unspecified",
			Allow: []string{"POST =/@cf/cloudflare/clef", "POST =/@cf/cloudflare/clef-flash"},
		},
		{
			ID: "mistral", Family: "inference", Label: "Mistral", Upstream: "https://api.mistral.ai/v1", Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots:    map[string]string{"transcription": "mistral"},
			Bindings: []Binding{{"providers.mistral.base_url", "providers.mistral.api_key"}},
		},
		{
			ID: "exa", Family: "search", Label: "Exa", Upstream: "https://api.exa.ai",
			Header: "x-api-key", Billing: "pay_as_you_go",
			Slots: map[string]string{"search": "exa"}, Allow: []string{"POST /search"},
			Bindings: []Binding{{"search.exa.base_url", "search.exa.api_key"}},
		},
		{
			ID: "exa-mcp", Family: "search", Parent: "exa", Label: "Exa MCP", Upstream: "https://mcp.exa.ai",
			Header: "x-api-key", Billing: "pay_as_you_go", PathSuffix: "/mcp",
			Slots: map[string]string{"search": "exa_mcp", "fetch": "exa_mcp"}, Allow: []string{"POST /mcp", "GET /mcp", "DELETE /mcp"},
			Bindings: []Binding{{"search.exa_mcp.url", "search.exa_mcp.api_key"}},
		},
		{
			ID: "brave", Family: "search", Label: "Brave Search", Upstream: "https://api.search.brave.com/res/v1",
			Header: "X-Subscription-Token", Billing: "pay_as_you_go",
			Slots: map[string]string{"search": "brave"}, Allow: []string{"GET /web/search"},
			Bindings: []Binding{{"search.brave.base_url", "search.brave.api_key"}},
		},
		{
			ID: "tavily", Family: "search", Label: "Tavily", Upstream: "https://api.tavily.com",
			Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots: map[string]string{"search": "tavily"}, Allow: []string{"POST /search"},
			Bindings: []Binding{{"search.tavily.base_url", "search.tavily.api_key"}},
		},
		{
			ID: "perplexity", Family: "search", Label: "Perplexity", Upstream: "https://api.perplexity.ai",
			Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots: map[string]string{"search": "perplexity"}, Allow: []string{"POST /search"},
			Bindings: []Binding{{"search.perplexity.base_url", "search.perplexity.api_key"}},
		},
		{
			ID: "parallel", Family: "search", Label: "Parallel", Upstream: "https://api.parallel.ai/v1",
			Header: "x-api-key", Billing: "pay_as_you_go",
			Slots: map[string]string{"search": "parallel"}, Allow: []string{"POST /search"},
			Bindings: []Binding{{"search.parallel.base_url", "search.parallel.api_key"}},
		},
		{
			ID: "youcom", Family: "search", Label: "You.com", Upstream: "https://ydc-index.io/v1",
			Header: "X-API-Key", Billing: "pay_as_you_go",
			Slots: map[string]string{"search": "youcom"}, Allow: []string{"POST /search"},
			Bindings: []Binding{{"search.youcom.base_url", "search.youcom.api_key"}},
		},
		{
			ID: "keenable", Family: "search", Label: "Keenable", Upstream: "https://api.keenable.ai/v1",
			Header: "X-API-Key", Billing: "pay_as_you_go",
			Slots: map[string]string{"search": "keenable"}, Allow: []string{"POST /search"},
			Bindings: []Binding{{"search.keenable.base_url", "search.keenable.api_key"}},
		},
		{
			ID: "sofya", Family: "search", Label: "Sofya", Upstream: "https://sofya.co/v1",
			Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots: map[string]string{"search": "sofya"}, Allow: []string{"POST =/search"},
			Bindings: []Binding{{"search.sofya.base_url", "search.sofya.api_key"}},
		},
		{
			ID: "webz", Family: "search", Label: "Webz.io News Search", Upstream: "https://api.webz.io",
			Header: "Authorization", Prefix: "Bearer ", Billing: "pay_as_you_go",
			Slots: map[string]string{"search": "webz"}, Allow: []string{"POST =/api/news/context"},
			Bindings: []Binding{{"search.webz.base_url", "search.webz.api_key"}},
		},
	}
}

// ForProvider reports the explicit proxy decision for a provider type.
func ForProvider(kind providerspec.Type) (Profile, bool) {
	for _, p := range All() {
		if p.Provider == kind && kind != "" {
			return p, true
		}
	}
	return Profile{}, false
}

// ByID reports the routing profile with the given ID, if registered.
func ByID(id string) (Profile, bool) {
	for _, p := range All() {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// InferenceCapable reports whether the profile binds a named provider, either
// through a registered provider type or through an explicit providers binding.
func InferenceCapable(p Profile) bool {
	if p.Provider != "" {
		return true
	}
	for _, b := range p.Bindings {
		if rest, ok := strings.CutPrefix(b.Root, "providers."); ok {
			if name, field, ok := strings.Cut(rest, "."); ok && name != "" && field != "" {
				return true
			}
		}
	}
	return false
}

// Companions returns the profiles whose primary is parentID, in catalog order.
func Companions(parentID string) []Profile {
	var companions []Profile
	for _, p := range All() {
		if p.Parent != "" && p.Parent == parentID {
			companions = append(companions, p)
		}
	}
	return companions
}

// CanonicalParent returns a companion's primary ID, or id itself for a primary
// or an unknown ID.
func CanonicalParent(id string) string {
	if p, ok := ByID(id); ok && p.Parent != "" {
		return p.Parent
	}
	return id
}
