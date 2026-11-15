package config

import "testing"

func TestLoadKeenableSearchConfig(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("KEENABLE_API_KEY", "env-key")
	writeTestConfig(t, configHome, `search:
  provider: keenable
  keenable:
    api_key: config-key
  youcom:
    api_key: youcom-key
`)
	cfg, err := loadUnresolved()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Search.Provider != "keenable" || cfg.Search.YouCom.APIKey != "youcom-key" {
		t.Fatalf("search config = %+v", cfg.Search)
	}
	if got, err := cfg.Search.KeenableKey().Resolve(); err != nil || got != "config-key" {
		t.Fatalf("KeenableKey = %q, err %v", got, err)
	}
}
