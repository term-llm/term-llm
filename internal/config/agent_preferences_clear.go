package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ClearAgentPreferences removes selected preference keys for an agent. With no
// keys it removes all of the agent's preferences. Empty preference mappings are
// pruned, and unrelated settings and comments are preserved.
func ClearAgentPreferences(agentName string, keys ...string) error {
	for _, key := range keys {
		if !KnownAgentPreferenceKeys[key] {
			return fmt.Errorf("unknown agent preference key: %s", key)
		}
	}
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		return nil // Nothing to clear; don't create a config file.
	}
	return editConfigFile(func(root *yaml.Node) (bool, error) {
		agents := lookupMapping(root, "agents")
		if agents == nil {
			return false, nil
		}
		prefs := lookupMapping(agents, "preferences")
		if prefs == nil {
			return false, nil
		}
		changed := false
		if len(keys) == 0 {
			changed = deleteMappingKey(prefs, agentName)
		} else if i := mappingValueIndex(prefs, agentName); i >= 0 && prefs.Content[i].Kind == yaml.MappingNode {
			pref := prefs.Content[i]
			for _, key := range keys {
				if deleteMappingKey(pref, key) {
					changed = true
				}
			}
			if changed && len(pref.Content) == 0 {
				deleteMappingKey(prefs, agentName)
			}
		}
		if changed && len(prefs.Content) == 0 {
			deleteMappingKey(agents, "preferences")
		}
		return changed, nil
	})
}
