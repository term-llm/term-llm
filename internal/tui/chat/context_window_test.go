package chat

import (
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/term-llm/term-llm/internal/config"
	"github.com/term-llm/term-llm/internal/llm/core"
	"github.com/term-llm/term-llm/internal/llm/engine"
	"github.com/term-llm/term-llm/internal/llm/mockprovider"
	"github.com/term-llm/term-llm/internal/session"
	"github.com/term-llm/term-llm/internal/ui"
)

func reportedWindowSession() *session.Session {
	return &session.Session{ID: session.NewID(), Provider: "claude-bin", ProviderKey: "claude-bin", Model: "opus", Mode: session.ModeChat, CreatedAt: time.Now(), UpdatedAt: time.Now(), CompactionSeq: -1}
}

func reportedWindowChat(store session.Store, s *session.Session) (*Model, *mockprovider.MockProvider) {
	p := mockprovider.NewMockProvider("claude-bin").WithCapabilities(core.Capabilities{ManagesOwnContext: true})
	return newReportedWindowChat(store, s, p, engine.NewEngine(p, nil)), p
}

func newReportedWindowChat(store session.Store, s *session.Session, p *mockprovider.MockProvider, e *engine.Engine) *Model {
	return New(&config.Config{DefaultProvider: "claude-bin"}, p, e, "claude-bin", "opus", nil, 20, false, false, false, nil, "", "", false, "", store, s, false, nil, false, false, "", "", false)
}

func streamWindowReading(t *testing.T, m *Model, p *mockprovider.MockProvider, reading core.ContextUsage) {
	t.Helper()
	p.AddTurn(mockprovider.MockTurn{Text: "done", Context: &reading})
	stream, err := m.engine.Stream(t.Context(), core.Request{Messages: []core.Message{core.UserText("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := stream.Recv(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if got := m.engine.ContextUsage(); got != reading {
		t.Fatalf("live reading = %+v, want %+v", got, reading)
	}
}

func TestTUIContextWindowStreamDoneSQLiteReopen(t *testing.T) {
	for _, tc := range []struct {
		name, savedProvider, savedModel string
		wantWindow                      int
	}{
		{"matching identity", "claude-bin", "opus", 1000000},
		{"different provider", "other-provider", "opus", 0},
		{"different model", "claude-bin", "sonnet", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := session.Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")}
			store, err := session.NewSQLiteStore(cfg)
			if err != nil {
				t.Fatal(err)
			}
			s := reportedWindowSession()
			s.ProviderKey, s.Model = tc.savedProvider, tc.savedModel
			if err := store.Create(t.Context(), s); err != nil {
				t.Fatal(err)
			}
			m, p := reportedWindowChat(store, s)
			reading := core.ContextUsage{UsedTokens: 634800, Window: 1000000}
			streamWindowReading(t, m, p, reading)
			m.streaming = true
			m.Update(streamEventMsg{event: ui.DoneEvent(42)})
			if m.sess.LastTotalTokens != reading.UsedTokens || m.sess.LastContextWindow != tc.wantWindow {
				t.Errorf("stream done: in-memory estimate = %d/%d, want %d/%d", m.sess.LastTotalTokens, m.sess.LastContextWindow, reading.UsedTokens, tc.wantWindow)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := session.NewSQLiteStore(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			saved, err := reopened.Get(t.Context(), s.ID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.LastTotalTokens != reading.UsedTokens || saved.LastContextWindow != tc.wantWindow {
				t.Errorf("reopened SQLite estimate = %d/%d, want %d/%d", saved.LastTotalTokens, saved.LastContextWindow, reading.UsedTokens, tc.wantWindow)
			}
		})
	}
}

func TestTUIContextWindowRestore(t *testing.T) {
	want := core.ContextUsage{UsedTokens: 12000, Window: 200000}
	saved := reportedWindowSession()
	saved.LastTotalTokens, saved.LastContextWindow = want.UsedTokens, want.Window
	t.Run("cold resume", func(t *testing.T) {
		m, _ := reportedWindowChat(nil, saved)
		if got := m.engine.ContextUsage(); got != want {
			t.Fatalf("restored = %+v, want %+v", got, want)
		}
	})
	t.Run("switch sessions on same model", func(t *testing.T) {
		m, p := reportedWindowChat(nil, reportedWindowSession())
		streamWindowReading(t, m, p, core.ContextUsage{UsedTokens: 4000, Window: 1000000})
		m.Update(sessionLoadedMsg{sess: saved})
		if got := m.engine.ContextUsage(); got != want {
			t.Fatalf("selected session = %+v, want %+v", got, want)
		}
	})
	t.Run("stale row cannot replace newer unknown window", func(t *testing.T) {
		m, p := reportedWindowChat(nil, saved)
		live := core.ContextUsage{UsedTokens: 6000}
		streamWindowReading(t, m, p, live)
		m.configureContextManagementForSession()
		if got := m.engine.ContextUsage(); got != live {
			t.Fatalf("after reconfiguration = %+v, want live %+v", got, live)
		}
	})
}

func TestTUIContextWindowRestoreUsesActiveIdentity(t *testing.T) {
	for _, tc := range []struct{ provider, model string }{{"other-provider", "opus"}, {"claude-bin", "other-model"}} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			saved := reportedWindowSession()
			saved.ProviderKey = tc.provider
			saved.Model = tc.model
			saved.LastTotalTokens, saved.LastContextWindow = 12000, 200000
			m, _ := reportedWindowChat(nil, saved) // runtime remains claude-bin:opus
			if got := m.engine.ContextUsage(); got.Window != 0 {
				t.Fatalf("active runtime inherited saved window: %+v", got)
			}
		})
	}
}

func TestTUIContextWindowClearedOnSessionRelabel(t *testing.T) {
	for _, kind := range []string{"model", "effort"} {
		t.Run(kind, func(t *testing.T) {
			saved := reportedWindowSession()
			saved.LastTotalTokens, saved.LastContextWindow = 12000, 200000
			m, _ := reportedWindowChat(nil, saved)
			switch kind {
			case "model":
				m.switchModel("debug:fast")
			case "effort":
				m.switchEffortStateOnly(effortSwitchResolution{provider: "claude-bin", targetModel: "opus-high", label: "high", ok: true}, false)
			}
			if m.sess.Model == "opus" {
				t.Fatal("session did not switch models")
			}
			if m.sess.LastContextWindow != 0 {
				t.Fatalf("relabeled metadata kept window %d", m.sess.LastContextWindow)
			}
			m.configureContextManagementForSession()
			if _, _, window := m.engine.ContextEstimateSnapshot(); window != 0 {
				t.Fatalf("configured new model with old reported window %d", window)
			}
		})
	}
}

// The TUI may be given an engine that has already observed the active turn.
// An older session row (including one with no estimate) must not clear it.
func TestTUIContextWindowNewPreservesLiveEngine(t *testing.T) {
	for _, used := range []int{0, 12000} {
		t.Run(fmt.Sprint(used), func(t *testing.T) {
			saved := reportedWindowSession()
			saved.LastTotalTokens = used
			if used > 0 {
				saved.LastContextWindow = 200000
			}
			first, p := reportedWindowChat(nil, saved)
			live := core.ContextUsage{UsedTokens: 6000}
			streamWindowReading(t, first, p, live)
			resumed := newReportedWindowChat(nil, saved, p, first.engine)
			if got := resumed.engine.ContextUsage(); got != live {
				t.Fatalf("New replaced live reading %+v with %+v", live, got)
			}
		})
	}
}
