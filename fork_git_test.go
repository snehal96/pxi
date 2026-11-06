package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pxiPost(t *testing.T, h http.Handler, path, body string) (int, map[string]any) {
	t.Helper()
	rec := serve(h, "POST", path, "127.0.0.1:7777", "http://127.0.0.1:7777", body)
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func TestPxiStashesBranchesRemotes(t *testing.T) {
	root := nestedRepo(t) // main + a worktree in .claude/worktrees/fix (fork_nested_test.go)
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitTestRun(t, root, "init", "-q", "--bare", bare)
	gitTestRun(t, root, "remote", "add", "origin", bare)
	gitTestRun(t, root, "push", "-q", "origin", "main")
	gitTestRun(t, root, "tag", "-a", "v1.0", "-m", "first release")

	s := wtServer(t, root)
	h := hostGuard(s)

	// --- stash: tracked + untracked, never the nested worktree
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main // edited\n"), 0o644)
	os.WriteFile(filepath.Join(root, "scratch.txt"), []byte("notes\n"), 0o644)
	if code, m := pxiPost(t, h, "/api/pxi/stash", `{"action":"push","message":"wip login","untracked":true}`); code != 200 {
		t.Fatalf("stash push: %d %v", code, m)
	}
	if _, err := os.Stat(filepath.Join(root, "scratch.txt")); err == nil {
		t.Error("untracked file was not stashed")
	}
	if _, err := os.Stat(filepath.Join(root, ".claude/worktrees/fix/main.go")); err != nil {
		t.Fatal("nested worktree was stashed away")
	}
	code, m := get(t, s, "/api/pxi/stashes")
	list, _ := m["stashes"].([]any)
	if code != 200 || len(list) != 1 {
		t.Fatalf("stashes: %d %v", code, m)
	}
	st := list[0].(map[string]any)
	if st["ref"] != "stash@{0}" || st["message"] != "wip login" || st["branch"] != "main" {
		t.Errorf("stash entry: %v", st)
	}
	// Tracked changes against the stash's first parent, untracked files apart.
	_, sf := get(t, s, "/api/pxi/stash-files?sha="+st["sha"].(string))
	files, _ := sf["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("stash files: %v", sf)
	}
	f0, f1 := files[0].(map[string]any), files[1].(map[string]any)
	if f0["path"] != "main.go" || f0["status"] != "M" || f1["path"] != "scratch.txt" || f1["status"] != "U" {
		t.Errorf("stash files: %v", files)
	}
	// Each opens as a two-commit diff.
	_, d := get(t, s, "/api/diff?path=main.go&ref="+f0["ref"].(string)+"&base="+f0["base"].(string))
	if diff, _ := d["diff"].(string); !strings.Contains(diff, "+package main // edited") || strings.Count(diff, "diff --git") != 1 {
		t.Errorf("stash diff: %q", d["diff"])
	}
	_, d = get(t, s, "/api/diff?path=scratch.txt&ref="+f1["ref"].(string)+"&base="+f1["base"].(string))
	if diff, _ := d["diff"].(string); !strings.Contains(diff, "+notes") {
		t.Errorf("untracked stash diff: %q", d["diff"])
	}
	if code, m := pxiPost(t, h, "/api/pxi/stash", `{"action":"pop","ref":"stash@{0}"}`); code != 200 {
		t.Fatalf("stash pop: %d %v", code, m)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "main.go")); !strings.Contains(string(b), "edited") {
		t.Error("pop did not restore the change")
	}
	if code, _ := pxiPost(t, h, "/api/pxi/stash", `{"action":"drop","ref":"--all"}`); code != 400 {
		t.Errorf("bad stash ref accepted: %d", code)
	}

	// --- branches: create, list (with the worktree's branch), switch, delete
	gitTestRun(t, root, "stash", "-q", "--include-untracked")
	if code, m := pxiPost(t, h, "/api/pxi/branch", `{"action":"create","name":"feat/x"}`); code != 200 {
		t.Fatalf("create: %d %v", code, m)
	}
	_, bm := get(t, s, "/api/pxi/branches")
	byName := map[string]map[string]any{}
	for _, b := range bm["local"].([]any) {
		byName[b.(map[string]any)["name"].(string)] = b.(map[string]any)
	}
	if byName["feat/x"]["current"] != true {
		t.Errorf("feat/x not current: %v", byName)
	}
	if wt, _ := byName["fix"]["worktree"].(string); !strings.HasSuffix(wt, ".claude/worktrees/fix") {
		t.Errorf("fix branch should say which worktree has it: %v", byName["fix"])
	}
	if remote, _ := bm["remote"].([]any); len(remote) != 1 || remote[0].(map[string]any)["name"] != "origin/main" {
		t.Errorf("remote branches: %v", bm["remote"])
	}
	if code, _ := pxiPost(t, h, "/api/pxi/branch", `{"action":"switch","name":"main"}`); code != 200 {
		t.Fatalf("switch: %d", code)
	}
	if code, m := pxiPost(t, h, "/api/pxi/branch", `{"action":"delete","name":"feat/x"}`); code != 200 {
		t.Fatalf("delete: %d %v", code, m)
	}
	if code, _ := pxiPost(t, h, "/api/pxi/branch", `{"action":"create","name":"bad..name"}`); code != 400 {
		t.Errorf("invalid branch name accepted: %d", code)
	}
	if code, _ := pxiPost(t, h, "/api/pxi/branch", `{"action":"switch","name":"--orphan"}`); code != 400 {
		t.Errorf("option-like name accepted: %d", code)
	}

	// --- remotes, tags, fetch
	_, rm := get(t, s, "/api/pxi/remotes")
	if r, _ := rm["remotes"].([]any); len(r) != 1 || r[0].(map[string]any)["name"] != "origin" {
		t.Errorf("remotes: %v", rm)
	}
	if tg, _ := rm["tags"].([]any); len(tg) != 1 || tg[0].(map[string]any)["name"] != "v1.0" || tg[0].(map[string]any)["sha"] == "" {
		t.Errorf("tags: %v", rm["tags"])
	}
	if code, m := pxiPost(t, h, "/api/pxi/fetch", `{"remote":"origin"}`); code != 200 {
		t.Errorf("fetch: %d %v", code, m)
	}

	// --- cross-site requests are refused
	rec := serve(h, "POST", "/api/pxi/branch", "127.0.0.1:7777", "http://evil.example", `{"action":"delete","name":"main"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site branch delete: %d", rec.Code)
	}
}
