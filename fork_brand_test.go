package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateSettingsFromPx0CopiesOnce(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	old := filepath.Join(cfg, "px0", "settings.json")
	os.MkdirAll(filepath.Dir(old), 0o755)
	os.WriteFile(old, []byte(`{"theme":"nord"}`), 0o644)

	migrateSettingsFromPx0()
	got, err := os.ReadFile(filepath.Join(cfg, "pxi", "settings.json"))
	if err != nil || string(got) != `{"theme":"nord"}` {
		t.Fatalf("not copied: %q %v", got, err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("px0's settings must stay where they are")
	}
	// pxi's own settings are never overwritten afterwards.
	os.WriteFile(filepath.Join(cfg, "pxi", "settings.json"), []byte(`{"theme":"dracula"}`), 0o644)
	migrateSettingsFromPx0()
	if got, _ := os.ReadFile(filepath.Join(cfg, "pxi", "settings.json")); string(got) != `{"theme":"dracula"}` {
		t.Fatalf("overwrote pxi settings: %q", got)
	}
}
