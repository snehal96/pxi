package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPxiIgnoredEnvFilesAreFindable(t *testing.T) {
	root := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	w(".gitignore", ".env*\nnode_modules/\n*.log\n.DS_Store\n")
	w("app/main.go", "package main\n")
	w("app/.env", "A=1\n")
	w("app/.env.local", "A=2\n")
	w("app/debug.log", "x\n")
	w("app/.DS_Store", "x\n")
	w("node_modules/pkg/.env", "A=3\n")
	ix := NewIndex(root)
	ix.Build()
	have := map[string]bool{}
	for _, f := range ix.files {
		have[f.Path] = true
	}
	for _, p := range []string{"app/.env", "app/.env.local", "app/main.go"} {
		if !have[p] {
			t.Errorf("%s not indexed", p)
		}
	}
	for _, p := range []string{"app/debug.log", "app/.DS_Store", "node_modules/pkg/.env"} {
		if have[p] {
			t.Errorf("%s indexed", p)
		}
	}
}
