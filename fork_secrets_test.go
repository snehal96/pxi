package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"px0/internal/settings"
)

func TestPxiSettingsNeverReturnTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Dir(settings.Path()), 0o700)
	os.WriteFile(settings.Path(), []byte("{\n  \"github.token\": \"ghp_SECRETVALUE1234\",\n  \"editor.fontSize\": 14\n}\n"), 0o600)
	s, _ := newTestServer(t)
	h := hostGuard(s)

	rec := serve(h, "GET", "/api/settings", "127.0.0.1:7777", "", "")
	if strings.Contains(rec.Body.String(), "SECRETVALUE") {
		t.Fatalf("token leaked: %s", rec.Body.String())
	}
	var j struct {
		Settings map[string]any `json:"settings"`
		Raw      string         `json:"raw"`
	}
	json.Unmarshal(rec.Body.Bytes(), &j)
	if j.Settings["github.token"] != "••••1234" || !strings.Contains(j.Raw, `"github.token": "••••1234"`) {
		t.Errorf("mask: %v / %s", j.Settings["github.token"], j.Raw)
	}

	// Saving the raw text back unchanged (with its mask) keeps the token.
	body, _ := json.Marshal(map[string]any{"raw": strings.Replace(j.Raw, "14", "15", 1)})
	if rec := serve(h, "POST", "/api/settings", "127.0.0.1:7777", "http://127.0.0.1:7777", string(body)); rec.Code != 200 {
		t.Fatalf("raw save: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := settings.ReadMergedMap()["github.token"].(string); got != "ghp_SECRETVALUE1234" {
		t.Errorf("raw save lost the token: %q", got)
	}
	// The form sending the mask keeps it too; a new value replaces it.
	serve(h, "POST", "/api/settings", "127.0.0.1:7777", "http://127.0.0.1:7777", `{"github.token":"••••1234"}`)
	if got, _ := settings.ReadMergedMap()["github.token"].(string); got != "ghp_SECRETVALUE1234" {
		t.Errorf("masked form save changed the token: %q", got)
	}
	serve(h, "POST", "/api/settings", "127.0.0.1:7777", "http://127.0.0.1:7777", `{"github.token":"ghp_NEW"}`)
	if got, _ := settings.ReadMergedMap()["github.token"].(string); got != "ghp_NEW" {
		t.Errorf("new token not saved: %q", got)
	}
}
