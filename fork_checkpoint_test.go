package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPxiCheckpoint(t *testing.T) {
	root, _, _ := wtRepo(t)
	s := wtServer(t, root)
	h := hostGuard(s)
	get := func() pxiCheckpointInfo {
		t.Helper()
		rec := serve(h, "GET", "/api/pxi/checkpoint", "127.0.0.1:7777", "", "")
		var info pxiCheckpointInfo
		json.Unmarshal(rec.Body.Bytes(), &info)
		return info
	}
	if get().Active {
		t.Fatal("active before any mark")
	}
	// A file already dirty and one untracked before the mark: neither counts.
	os.WriteFile(filepath.Join(root, "before.txt"), []byte("x"), 0o644)
	staged := gitTestRun(t, root, "diff", "--cached", "--name-only")
	if code, m := pxiPost(t, h, "/api/pxi/checkpoint", `{}`); code != 200 || m["active"] != true {
		t.Fatalf("mark: %d %v", code, m)
	}
	if got := get(); !got.Active || got.Count != 0 {
		t.Fatalf("right after the mark: %+v", got)
	}
	// Staging was left alone.
	if after := gitTestRun(t, root, "diff", "--cached", "--name-only"); after != staged {
		t.Errorf("index changed: %q -> %q", staged, after)
	}
	files := gitTestRun(t, root, "ls-files")
	first := filepath.Join(root, firstLine(files))
	os.WriteFile(first, []byte("changed\n"), 0o644)
	os.WriteFile(filepath.Join(root, "new.txt"), []byte("n"), 0o644)
	os.Remove(filepath.Join(root, "before.txt"))
	got := get()
	if got.Files[firstLine(files)] != "M" || got.Files["new.txt"] != "A" || got.Files["before.txt"] != "D" || got.Count != 3 {
		t.Fatalf("after edits: %+v", got)
	}
	// Committing keeps the change flagged; the working tree is what is compared.
	gitTestRun(t, root, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qam", "edit")
	if got := get(); got.Files[firstLine(files)] != "M" {
		t.Fatalf("after commit: %+v", got)
	}
	if code, _ := pxiPost(t, h, "/api/pxi/checkpoint", `{"clear":true}`); code != 200 || get().Active {
		t.Fatal("clear failed")
	}
	if rec := serve(h, "POST", "/api/pxi/checkpoint", "127.0.0.1:7777", "http://evil.example", `{}`); rec.Code == 200 {
		t.Error("cross-site mark accepted")
	}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
