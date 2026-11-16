package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPxiFindIgnored(t *testing.T) {
	d := t.TempDir()
	w := func(rel string) {
		p := filepath.Join(d, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	w(".gitignore")
	os.WriteFile(filepath.Join(d, ".gitignore"), []byte("tmp/\nnode_modules/\n"), 0o644)
	w("src/main.go")
	w("tmp/doc/agents-plan-2026-11-16.md")
	w("node_modules/pkg/agents-plan.md")
	ix := NewIndex(d)
	ix.Build()

	paths := func(q string) map[string]bool {
		out := map[string]bool{}
		for _, r := range pxiFindIgnored(ix, q, nil, 100) {
			out[r.Path] = true
		}
		return out
	}
	got := paths("agents-plan")
	if !got["tmp/doc/agents-plan-2026-11-16.md"] {
		t.Errorf("file in an ignored folder not found: %v", got)
	}
	if got["node_modules/pkg/agents-plan.md"] {
		t.Error("node_modules was searched")
	}
	if len(paths("ag")) != 0 {
		t.Error("a two-letter query read ignored folders")
	}
	if !paths("agentsplan:12")["tmp/doc/agents-plan-2026-11-16.md"] {
		t.Error("file:line query did not match")
	}
}
