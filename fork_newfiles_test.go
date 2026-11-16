package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func pxiIndexHas(ix *Index, p string) bool {
	for _, f := range ix.Files() {
		if f.Path == p {
			return true
		}
	}
	return false
}

func TestPxiSyncNewFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	d := t.TempDir()
	if r, err := filepath.EvalSymlinks(d); err == nil {
		d = r // macOS: /var is a symlink, git reports /private/var
	}
	if err := exec.Command("git", "-C", d, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(d, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(d, ".gitignore"), []byte("skip.log\n"), 0o644)
	ix := NewIndex(d)
	ix.Build()
	gw := NewGitWatcher(ix)

	os.MkdirAll(filepath.Join(d, "sub"), 0o755)
	os.WriteFile(filepath.Join(d, "new.txt"), []byte("n"), 0o644)
	os.WriteFile(filepath.Join(d, "sub", "x.go"), []byte("package x"), 0o644)
	os.WriteFile(filepath.Join(d, "skip.log"), []byte("ignored"), 0o644)
	gw.Refresh()
	for _, p := range []string{"new.txt", "sub/x.go"} {
		if !pxiIndexHas(ix, p) {
			t.Errorf("%s not indexed after a status change", p)
		}
	}
	if pxiIndexHas(ix, "skip.log") {
		t.Error("a gitignored file was indexed")
	}
	if kids, _ := ix.Children("sub"); len(kids) != 1 || kids[0].Path != "sub/x.go" {
		t.Errorf("sub children = %v", kids)
	}

	os.Remove(filepath.Join(d, "new.txt"))
	gw.Refresh()
	if pxiIndexHas(ix, "new.txt") {
		t.Error("a deleted untracked file stayed in the index")
	}
	if !pxiIndexHas(ix, "sub/x.go") {
		t.Error("sub/x.go dropped")
	}
}
