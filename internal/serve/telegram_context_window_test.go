package serve

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/mockprovider"
	"github.com/term-llm/term-llm/internal/session"
	"github.com/term-llm/term-llm/internal/testutil"
)

func TestTelegramContextWindowWriter(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(session.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			_ = store.Close()
		}
	}()
	h := testutil.NewEngineHarness()
	mgr, template := newTestMgrAndSession(h)
	template.runtime.ProviderName = "Claude CLI (test)"
	template.runtime.ProviderKey = "claude-bin"
	mgr.store = store
	mgr.settings.NewSession = func(context.Context) (*SessionRuntime, error) { return template.runtime, nil }
	sess, err := mgr.getOrCreate(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	for _, reading := range []core.ContextUsage{{UsedTokens: 12100, Window: 200000}, {UsedTokens: 302, Window: 0}} {
		h.Provider.AddTurn(mockprovider.MockTurn{Text: "done", Context: &reading})
		if err := mgr.streamReply(ctx, &fakeBotSender{}, sess, 42, core.UserText("hello")); err != nil {
			t.Fatal(err)
		}
		check := func(source string, meta *session.Session) {
			t.Helper()
			if meta.LastTotalTokens != reading.UsedTokens || meta.LastMessageCount != 0 || meta.LastContextWindow != reading.Window {
				t.Errorf("%s: got context (%d, %d, %d), want (%d, 0, %d)", source, meta.LastTotalTokens, meta.LastMessageCount, meta.LastContextWindow, reading.UsedTokens, reading.Window)
			}
		}
		check("reload metadata", sess.meta)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err = session.NewSQLiteStore(session.Config{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		mgr.store = store
		saved, err := store.Get(ctx, sess.meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		check("reopened SQLite", saved)
		if saved.ProviderKey != "claude-bin" || saved.Provider != "Claude CLI (test)" {
			t.Fatalf("persisted identity = %q / %q", saved.ProviderKey, saved.Provider)
		}
		if reading.Window != 0 {
			freshMgr, fresh := newTestMgrAndSession(testutil.NewEngineHarness())
			fresh.runtime.ProviderKey, fresh.runtime.ProviderName = saved.ProviderKey, saved.Provider
			freshMgr.store = store
			freshMgr.settings.TelegramCarryoverChars = 4096
			freshMgr.settings.NewSession = func(context.Context) (*SessionRuntime, error) { return fresh.runtime, nil }
			next, err := freshMgr.getOrCreate(ctx, 42)
			if err != nil {
				t.Fatal(err)
			}
			if next.meta.ID == saved.ID || next.carryoverMessageCount == 0 {
				t.Fatal("expected a new session with transcript carryover")
			}
			if total, _, window := next.runtime.Engine.ContextEstimateSnapshot(); total != 0 || window != 0 {
				t.Fatalf("new session inherited context %d / %d", total, window)
			}
		}
	}
}

func TestTelegramContextWindowReload(t *testing.T) {
	saved := core.ContextUsage{UsedTokens: 12100, Window: 200000}
	live := core.ContextUsage{UsedTokens: 302}
	for _, tc := range []struct {
		name, model, key string
		live             bool
		want             core.ContextUsage
	}{
		{"same model", "test", "claude-bin", false, saved},
		{"new model", "other", "claude-bin", false, core.ContextUsage{}},
		{"different provider with same label", "test", "other-key", false, core.ContextUsage{}},
		{"newer reading with unknown window", "test", "claude-bin", true, live},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testutil.NewEngineHarness()
			h.Provider.WithCapabilities(core.Capabilities{ManagesOwnContext: true})
			h.Engine.ConfigureContextManagement(h.Provider, tc.key, tc.model, false)
			mgr, template := newTestMgrAndSession(h)
			template.runtime.ModelName = tc.model
			template.runtime.ProviderName, template.runtime.ProviderKey = "Claude CLI ("+tc.model+")", tc.key
			mgr.settings.NewSession = func(context.Context) (*SessionRuntime, error) { return template.runtime, nil }
			if tc.live {
				h.Provider.AddTurn(mockprovider.MockTurn{Text: "done", Context: &live})
				if _, err := h.Run(t.Context(), core.Request{Model: tc.model, Messages: []core.Message{core.UserText("hello")}}); err != nil {
					t.Fatal(err)
				}
			}
			mgr.restored = map[int64]telegramReloadChat{42: {Meta: &session.Session{ID: "saved", Provider: "Claude CLI (test)", ProviderKey: "claude-bin", Model: "test", LastTotalTokens: saved.UsedTokens, LastContextWindow: saved.Window}}}
			sess, err := mgr.getOrCreate(t.Context(), 42)
			if err != nil {
				t.Fatal(err)
			}
			got := sess.runtime.Engine.ContextUsage()
			if got != tc.want {
				t.Errorf("restored context = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTelegramContextWindowWriterAfterModelChange(t *testing.T) {
	ctx := t.Context()
	cfg := session.Config{Path: filepath.Join(t.TempDir(), "sessions.db")}
	store, err := session.NewSQLiteStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := testutil.NewEngineHarness()
	mgr, template := newTestMgrAndSession(h)
	mgr.store = store
	mgr.settings.NewSession = func(context.Context) (*SessionRuntime, error) { return template.runtime, nil }
	original, err := mgr.getOrCreate(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PersistContextEstimate(ctx, store, original.meta, 100, 0, 1000); err != nil {
		t.Fatal(err)
	}
	// The reload keeps the session ID and model in metadata, but builds a runtime for the new model.
	mgr, template = newTestMgrAndSession(h)
	mgr.store = store
	template.runtime.ModelName = "replacement"
	mgr.settings.NewSession = func(context.Context) (*SessionRuntime, error) { return template.runtime, nil }
	mgr.restored = map[int64]telegramReloadChat{42: {Meta: original.meta}}
	active, err := mgr.getOrCreate(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	h.Provider.AddTurn(mockprovider.MockTurn{Text: "done", Context: &core.ContextUsage{UsedTokens: 200, Window: 2000}})
	if err := mgr.streamReply(ctx, &fakeBotSender{}, active, 42, core.UserText("hello")); err != nil {
		t.Fatal(err)
	}
	if got := active.runtime.Engine.ContextUsage(); got.Window != 2000 {
		t.Fatalf("new model did not report a window: %+v", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := session.NewSQLiteStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.Get(ctx, active.meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range []*session.Session{active.meta, saved} {
		if meta.LastTotalTokens != 200 || meta.LastMessageCount != 0 || meta.LastContextWindow != 0 {
			t.Errorf("old model %q estimate after replacement turn = (%d, %d, %d), want (200, 0, 0)", meta.Model, meta.LastTotalTokens, meta.LastMessageCount, meta.LastContextWindow)
		}
	}
}
