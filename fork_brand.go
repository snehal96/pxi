package main

// pxi keeps its settings and state in ~/.pxi (or $XDG_CONFIG_HOME/pxi), apart
// from an installed px0. On the first run, settings.json is copied from px0's
// folder so the theme, harness choice and forge tokens carry over. Nothing is
// moved or deleted; px0 keeps working as before.

import (
	"os"
	"path/filepath"

	"px0/internal/settings"
)

func migrateSettingsFromPx0() {
	dst := settings.Path()
	if dst == "" {
		return
	}
	if _, err := os.Stat(dst); err == nil {
		return // pxi already has its own
	}
	var src string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		src = filepath.Join(xdg, "px0", "settings.json")
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		src = filepath.Join(home, ".px0", "settings.json")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(dst), 0o755) != nil {
		return
	}
	_ = os.WriteFile(dst, data, 0o600) // may hold forge tokens
}
