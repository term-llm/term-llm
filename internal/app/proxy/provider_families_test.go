package proxy

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/proxyclient"
	"github.com/term-llm/term-llm/internal/proxyprofile"
)

func TestProviderStructure(t *testing.T) {
	makeConfig := func() Config {
		return Config{Providers: map[string]Provider{"exa": {Label: "Exa"}}, Routes: []Route{
			{Name: "rest", Provider: "exa", Profile: "exa"}, {Name: "mcp", Provider: "exa", Profile: "exa-mcp"},
		}, Defaults: map[string]string{"search": "rest", "fetch": "mcp"}}
	}
	for _, tt := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"valid", nil},
		{"name collision", func(c *Config) {
			c.Providers["rest"] = Provider{}
			c.Routes = append(c.Routes, Route{Name: "other", Provider: "rest"})
		}},
		{"empty provider", func(c *Config) { c.Providers["empty"] = Provider{} }},
		{"missing provider", func(c *Config) { c.Routes[0].Provider = "" }},
		{"unknown profile", func(c *Config) { c.Routes[0].Profile = "typo" }},
		{"noncanonical search", func(c *Config) {
			c.Providers["my-exa"] = c.Providers["exa"]
			delete(c.Providers, "exa")
			for i := range c.Routes {
				c.Routes[i].Provider = "my-exa"
			}
		}},
		{"mixed companion sets", func(c *Config) { c.Routes[0].Profile = "brave" }},
		{"wrong slot", func(c *Config) { c.Defaults["fetch"] = "rest" }},
		{"embed excluded", func(c *Config) { c.Defaults["embed"] = "mcp" }},
		{"non-inference hints", func(c *Config) { c.Providers["exa"] = Provider{Client: &proxyprofile.ClientProviderConfig{}} }},
		{"stored family on profiled provider", func(c *Config) { c.Providers["exa"] = Provider{Family: "search"} }},
		{"builtin handmade without type", func(c *Config) {
			c.Providers["openai"] = Provider{}
			c.Routes = append(c.Routes, Route{Name: "custom-openai", Provider: "openai"})
		}},
		{"duplicate inference routes", func(c *Config) {
			c.Providers["openai-work"] = Provider{}
			c.Routes = append(c.Routes, Route{Name: "one", Provider: "openai-work", Profile: "openai"}, Route{Name: "two", Provider: "openai-work"})
		}},
		{"noncanonical default", func(c *Config) {
			c.Providers["openai-work"] = Provider{}
			c.Routes = append(c.Routes, Route{Name: "one", Provider: "openai-work", Profile: "openai"})
			c.Defaults["image"] = "one"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := makeConfig()
			if tt.mutate != nil {
				tt.mutate(&c)
			}
			if err := validateConfigStructure(c); (err != nil) != (tt.mutate != nil) {
				t.Fatalf("structural validation: %v", err)
			}
		})
	}
}

func TestStoredProviderStatusPrecedence(t *testing.T) {
	for _, tt := range []struct {
		name, upstream, state, reason     string
		disabled, partly, key, configured bool
		account                           AccountInfo
	}{
		{name: "disabled and missing key", upstream: "https://example.test", disabled: true, key: true, state: "red", reason: "API key missing"},
		{name: "loopback", upstream: "http://127.1.2.3", state: "green"},
		{name: "loopback ipv6", upstream: "http://[::1]", state: "green"},
		{name: "localhost", upstream: "http://localhost", state: "green"},
		{name: "unencrypted before partly disabled", upstream: "http://example.test", partly: true, state: "orange", reason: "Unencrypted upstream"},
		{name: "partly disabled", upstream: "https://example.test", partly: true, state: "orange", reason: "Partly disabled"},
		{name: "all disabled", upstream: "http://example.test", disabled: true, state: "grey", reason: "Disabled"},
		{name: "file-backed key from snapshot", upstream: "https://example.test", key: true, configured: true, state: "green"},
		{name: "unknown account state", upstream: "https://example.test", account: AccountInfo{Name: "a", State: "unknown"}, state: "red", reason: "Sign-in needed"},
		{name: "legacy native", upstream: "https://example.test", account: AccountInfo{Name: "a", Kind: "claude-bin", State: "ready"}, state: "red", reason: "Migration needed"},
		{name: "expired token", upstream: "https://example.test", account: AccountInfo{Name: "a", State: "ready", AuthMode: "token_only", ExpiresAt: time.Now().Unix() - 60}, state: "red", reason: "Sign-in needed"},
		{name: "refreshing", upstream: "http://example.test", account: AccountInfo{Name: "a", State: "refreshing"}, state: "orange", reason: "Refreshing sign-in"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := Route{Name: "primary", Provider: "exa", Profile: "exa", LocalPrefix: "/primary/", Upstream: tt.upstream, Enabled: !tt.disabled, AllowHTTP: true, Account: tt.account.Name}
			secrets := map[string]material{}
			if tt.key {
				r.SetHeaders = map[string]HeaderRule{"X-Key": {Secret: "key"}}
			}
			if tt.configured {
				secrets["key"] = material{Value: "file-backed"}
			}
			c := Config{Providers: map[string]Provider{"exa": {}}, Routes: []Route{r}}
			if tt.partly {
				c.Routes = append(c.Routes, Route{Name: "mcp", Provider: "exa", Profile: "exa-mcp", LocalPrefix: "/mcp/", Upstream: "https://example.test"})
			}
			snap, err := compile(c, 1, secrets)
			if err != nil {
				t.Fatal(err)
			}
			if state, reason := providerStatus("exa", snap.routes, accountsByName([]AccountInfo{tt.account}), snap.secrets); state != tt.state || reason != tt.reason {
				t.Fatalf("got %s (%s), want %s (%s)", state, reason, tt.state, tt.reason)
			}
		})
	}
}

func TestProviderDiscoveryV2ExactRoutes(t *testing.T) {
	s := testService(t)
	preset := func(id, name, provider string) Route {
		p, ok := routePresetByID(id)
		if !ok {
			t.Fatal(id)
		}
		return p.newRoute(provider, name, "")
	}
	var attempts atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1) }))
	defer up.Close()
	collision := Route{Name: "collision", Provider: "collision", LocalPrefix: "/mistral/", Upstream: up.URL, AllowHTTP: true, Enabled: true}
	configure(t, s, Config{Routes: []Route{preset("exa", "rest", "exa"), preset("exa-mcp", "mcp", "exa"), preset("chatgpt", "main", "chatgpt"), preset("chatgpt-backend", "side", "chatgpt"), preset("mistral", "mist", "mistral"), collision}, Defaults: map[string]string{"search": "rest", "fetch": "mcp"}}, nil)
	for _, tt := range []struct{ route, profile, slot string }{{"rest", "exa", "search"}, {"mcp", "exa-mcp", "fetch"}, {"side", "chatgpt-backend", ""}, {"mist", "mistral", ""}} {
		t.Run(tt.route, func(t *testing.T) {
			token := caller(t, s, tt.route)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/_proxy/client/v2/providers", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			s.ServeHTTP(w, r)
			out := proxyResponse[struct {
				Providers []clientProviderInfo             `json:"providers"`
				Defaults  map[string]clientProviderDefault `json:"defaults"`
			}](t, w, 200)
			if len(out.Providers) != 1 || len(out.Providers[0].Routes) != 1 || out.Providers[0].Routes[0].Profile != tt.profile {
				t.Fatalf("wrong projected routes: %+v", out.Providers)
			}
			if tt.slot == "" && len(out.Defaults) != 0 || tt.slot != "" && (len(out.Defaults) != 1 || out.Defaults[tt.slot] != (clientProviderDefault{"exa", tt.route})) {
				t.Fatalf("wrong defaults: %+v", out.Defaults)
			}
		})
	}
	for _, tt := range []struct {
		path    string
		code    int
		message string
	}{
		{"v1/providers", 410, "upgrade term-llm"},
		{"unavailable/mistral/v1/models", 403, "mistral is not granted to this client"},
		{"v6/providers", 404, "404 page not found"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/_proxy/client/"+tt.path, nil)
		r.Header.Set("Authorization", "Bearer "+caller(t, s, "collision"))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tt.code || !strings.Contains(w.Body.String(), tt.message) {
			t.Fatalf("%s: %d %s", tt.path, w.Code, w.Body)
		}
	}
	if attempts.Load() != 0 {
		t.Fatal("unavailable path reached upstream")
	}
}

func TestProviderAdminAtomicLifecycle(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	request := func(method, path string, body any, code int) {
		proxyResponse[map[string]any](t, adminRequest(s, method, path, body, cookie, csrf), code)
	}
	in := providerCreateInput{Revision: s.snap.revision, Name: "exa", Label: "Search", Preset: "exa", Value: "one-vendor-key"}
	request("POST", "providers", in, 201)
	request("POST", "providers", in, 409)
	stored, err := s.store.materials()
	if err != nil || len(stored) != 1 || stored["exa"].Value != "one-vendor-key" {
		t.Fatalf("storage: %+v %v", stored, err)
	}
	refs := routeSecretReferences(s.snap.config.Routes)
	if !slices.Equal(refs["exa"], []string{"exa", "exa-mcp"}) {
		t.Fatalf("wrong secret refs: %+v", refs)
	}
	request("PUT", "providers/exa", map[string]any{"revision": s.snap.revision, "label": "Renamed label"}, 200)
	state := proxyResponse[struct {
		Providers []adminProviderInfo `json:"providers"`
		Routes    []adminRouteInfo    `json:"routes"`
	}](t, adminRequest(s, "GET", "state", nil, cookie, csrf), 200)
	if len(state.Providers) != 1 || state.Providers[0].Label != "Renamed label" || state.Providers[0].Family != "search" || state.Providers[0].Status != "green" || len(state.Routes) != 2 || state.Routes[1].Provider != "exa" || state.Routes[1].Profile != "exa-mcp" || !slices.Contains(state.Routes[1].Serves, "search.exa_mcp.url") {
		t.Fatalf("incorrect admin state: %+v", state)
	}
	request("PUT", "defaults", map[string]any{"revision": s.snap.revision, "defaults": map[string]string{"search": "exa-mcp"}}, 200)
	request("DELETE", "providers/exa", map[string]any{"revision": s.snap.revision}, 200)
	stored, err = s.store.materials()
	if err != nil || len(stored) != 0 || len(s.snap.config.Providers) != 0 || len(s.snap.config.Routes) != 0 || len(s.snap.config.Defaults) != 0 {
		t.Fatalf("delete left data: %+v %+v %v", stored, s.snap.config, err)
	}

	// Invalid provider settings cannot leave a standalone secret behind.
	in.Revision, in.Name = s.snap.revision, "wrong-name"
	request("POST", "providers", in, 400)
	stored, err = s.store.materials()
	if err != nil || len(stored) != 0 || s.snap.revision != in.Revision {
		t.Fatalf("invalid provider changed durable state: %+v %v", stored, err)
	}

	// A provider delete must not remove a key another provider still uses.
	in.Name = "exa"
	request("POST", "providers", in, 201)
	request("POST", "providers", providerCreateInput{Revision: s.snap.revision, Name: "brave", Preset: "brave", Secret: "exa"}, 201)
	request("DELETE", "providers/exa", map[string]any{"revision": s.snap.revision}, 200)
	stored, err = s.store.materials()
	if err != nil || stored["exa"].Value != in.Value {
		t.Fatalf("delete removed a shared secret: %+v %v", stored, err)
	}
}

func TestDeletingLastRoutePrunesProviderAndDefault(t *testing.T) {
	s := testService(t)
	cookie, csrf := loginAdmin(t, s)
	configure(t, s, Config{Routes: []Route{{Name: "brave", Provider: "brave", Profile: "brave", LocalPrefix: "/brave/", Upstream: "https://example.test"}}, Defaults: map[string]string{"search": "brave"}}, nil)
	body := map[string]any{"revision": s.snap.revision, "routes": []Route{}, "providers": s.snap.config.Providers, "defaults": s.snap.config.Defaults}
	proxyResponse[map[string]int64](t, adminRequest(s, "PUT", "config", body, cookie, csrf), 200)
	if len(s.snap.config.Defaults) != 0 || len(s.snap.config.Providers) != 0 {
		t.Fatalf("route deletion left defaults or provider: %+v", s.snap.config)
	}
}

func TestSearchPresetsForwardOnlyVendorCredentials(t *testing.T) {
	for _, tt := range []struct {
		profile, provider, method, path, header, prefix string
		deny                                            string
	}{
		{"exa", "exa", "POST", "/search", "x-api-key", "", "/other"},
		{"exa-mcp", "exa", "POST", "/mcp", "x-api-key", "", "/unrelated"},
		{"brave", "brave", "GET", "/web/searchfoo", "X-Subscription-Token", "", "/images/search"},
		{"keenable", "keenable", "POST", "/search/public", "X-API-Key", "", "/account"},
		{"sofya", "sofya", "POST", "/search", "Authorization", "Bearer ", "/search/other"},
		{"webz", "webz", "POST", "/api/news/context", "Authorization", "Bearer ", "/api/news/other"},
		{"jina-reader", "jina", "GET", "/https://example.com/a%2Fb?x=1", "Authorization", "Bearer ", ""},
	} {
		t.Run(tt.profile, func(t *testing.T) {
			var calls, redirects atomic.Int32
			redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1) }))
			defer redirect.Close()
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.RequestURI() != tt.path || r.Method != tt.method {
					t.Errorf("wrong upstream request: %s %s", r.Method, r.URL.RequestURI())
				}
				if r.Header.Get(tt.header) != tt.prefix+"vendor-key" || tt.header != "Authorization" && r.Header.Get("Authorization") != "" {
					t.Error("the selected destination did not receive just its vendor credential")
				}
				http.Redirect(w, r, redirect.URL, http.StatusTemporaryRedirect)
			}))
			defer up.Close()
			s := testService(t)
			preset, ok := routePresetByID(tt.profile)
			if !ok {
				t.Fatal("missing preset")
			}
			r := preset.newRoute(tt.provider, tt.profile, "shared")
			r.AllowHTTP, r.Upstream = true, up.URL
			configure(t, s, Config{Routes: []Route{r}}, map[string]material{"shared": {Value: "vendor-key"}})
			server := httptest.NewServer(s)
			defer server.Close()
			root := server.URL + strings.TrimSuffix(r.LocalPrefix, "/")
			token := caller(t, s, r.Name)
			if err := proxyclient.Register([]proxyclient.Binding{{Root: root, Kind: tt.profile, Token: token}}); err != nil {
				t.Fatal(err)
			}
			check := func(method, target string, want int) {
				t.Helper()
				req, err := http.NewRequest(method, root+target, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set(tt.header, tt.prefix+token)
				resp, err := proxyclient.Do(server.Client(), req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != want {
					t.Fatalf("%s %s: got %d, want %d", method, target, resp.StatusCode, want)
				}
			}
			if tt.deny != "" {
				check(tt.method, tt.deny, 403)
			}
			check("PUT", tt.path, 403)
			check(tt.method, tt.path, http.StatusTemporaryRedirect)
			if calls.Load() != 1 || redirects.Load() != 0 {
				t.Fatalf("upstream calls=%d redirects=%d", calls.Load(), redirects.Load())
			}
		})
	}
}
