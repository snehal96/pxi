package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPxiGitignore(t *testing.T) {
	root, _, _ := wtRepo(t)
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	w := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	w("app/.gitignore", "tmp/\n")
	w("app/tmp/doc/plan.md", "p")
	w("notes/todo [draft].md", "t")
	w("scratch/a.txt", "a")
	s := wtServer(t, root)
	h := hostGuard(s)
	post := func(path, action string) map[string]any {
		t.Helper()
		code, m := sshPost(h, "/api/pxi/gitignore", `{"path":`+jsonStr(path)+`,"action":"`+action+`"}`)
		if code != 200 {
			t.Fatalf("%s %s: %d %v", action, path, code, m)
		}
		return m
	}
	read := func(rel string) string {
		b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		return string(b)
	}

	// Add, with a name that needs escaping; adding twice writes one line.
	if m := post("notes/todo [draft].md", "add"); m["ignored"] != true {
		t.Fatalf("add: %v", m)
	}
	post("notes/todo [draft].md", "add")
	if got := read(".gitignore"); strings.Count(got, `/notes/todo \[draft].md`) != 1 {
		t.Fatalf(".gitignore = %q", got)
	}
	// Remove the exact rule again.
	if m := post("notes/todo [draft].md", "remove"); m["ignored"] != false {
		t.Fatalf("remove: %v", m)
	}
	if strings.Contains(read(".gitignore"), "draft") {
		t.Fatal("rule not removed")
	}

	// Local: info/exclude, nothing in .gitignore.
	if m := post("scratch", "local"); m["ignored"] != true {
		t.Fatalf("local: %v", m)
	} else if rule := m["rule"].(map[string]any); rule["local"] != true {
		t.Fatalf("local rule: %v", rule)
	}
	if strings.Contains(read(".gitignore"), "scratch") {
		t.Fatal("local ignore written to .gitignore")
	}
	if m := post("scratch", "remove"); m["ignored"] != false {
		t.Fatalf("remove local: %v", m)
	}

	// A wider rule is reported, not edited.
	m := post("app/tmp/doc/plan.md", "remove")
	rule, _ := m["rule"].(map[string]any)
	if m["ignored"] != true || rule == nil || rule["file"] != "app/.gitignore" || rule["line"] != float64(1) || rule["pattern"] != "tmp/" {
		t.Fatalf("wide rule: %v", m)
	}
	if read("app/.gitignore") != "tmp/\n" {
		t.Fatal("a wide rule was edited")
	}
	// The folder the rule names exactly can be un-ignored.
	if m := post("app/tmp", "remove"); m["ignored"] != false {
		t.Fatalf("remove folder rule: %v", m)
	}

	// Outside the workspace and cross-site posts are refused.
	if code, _ := sshPost(h, "/api/pxi/gitignore", `{"path":"../x","action":"add"}`); code == 200 {
		t.Error("path outside the workspace accepted")
	}
	if rec := sshServe(h, "POST", "/api/pxi/gitignore", "http://evil.example", `{"path":"scratch","action":"add"}`); rec.Code == 200 {
		t.Error("cross-site post accepted")
	}
}
