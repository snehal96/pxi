package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// wtRepo makes a repository on main with two worktrees beside it:
//
//	wt-a: commits a change to shared.go and leaves new.go untracked
//	wt-b: changes shared.go without committing, and deletes gone.go
//
// so both worktrees overlap on shared.go. It returns the main checkout.
func wtRepo(t *testing.T) (root, a, b string) {
	t.Helper()
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	root = filepath.Join(dir, "platform")
	a = filepath.Join(dir, "wt-a")
	b = filepath.Join(dir, "wt-b")
	os.MkdirAll(root, 0o755)
	write := func(base, rel, body string) {
		p := filepath.Join(base, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(root, "shared.go", "package x\n\nvar A = 1\n")
	write(root, "gone.go", "package x\n")
	gitTestRun(t, root, "init", "-q", "-b", "main")
	gitTestRun(t, root, "config", "user.email", "t@example.com")
	gitTestRun(t, root, "config", "user.name", "T")
	gitTestRun(t, root, "config", "commit.gpgsign", "false")
	gitTestRun(t, root, "add", "-A")
	gitTestRun(t, root, "commit", "-qm", "init")
	gitTestRun(t, root, "worktree", "add", "-q", "-b", "feat-a", a)
	gitTestRun(t, root, "worktree", "add", "-q", "-b", "feat-b", b)

	write(a, "shared.go", "package x\n\nvar A = 2\n")
	gitTestRun(t, a, "commit", "-qam", "a")
	write(a, "new.go", "package x\n\nvar N = 1\n")

	write(b, "shared.go", "package x\n\nvar A = 3\n")
	os.Remove(filepath.Join(b, "gone.go"))
	return root, a, b
}

func wtServer(t *testing.T, root string) *Server {
	t.Helper()
	ix := NewIndex(root)
	ix.Build()
	return NewServer(ix, nil)
}

func wtGet(t *testing.T, s *Server, path string, q url.Values) (int, map[string]any) {
	t.Helper()
	return get(t, s, s.routePath(path)+"?"+q.Encode())
}

func TestWorktreesListsOthersWithChangesAndOverlaps(t *testing.T) {
	root, a, b := wtRepo(t)
	s := wtServer(t, root)
	code, m := wtGet(t, s, "/api/worktrees", url.Values{})
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, m)
	}
	list, _ := m["worktrees"].([]any)
	if len(list) != 2 {
		t.Fatalf("want 2 worktrees (main checkout excluded), got %d: %v", len(list), m)
	}
	byName := map[string]map[string]any{}
	for _, x := range list {
		w := x.(map[string]any)
		byName[w["name"].(string)] = w
	}
	wa, wb := byName["wt-a"], byName["wt-b"]
	if wa == nil || wb == nil {
		t.Fatalf("names: %v", byName)
	}
	if wa["path"] != a || wb["path"] != b {
		t.Errorf("paths: %v %v", wa["path"], wb["path"])
	}
	if wa["branch"] != "feat-a" || wa["baseRef"] != "main" {
		t.Errorf("wt-a branch/base: %v %v", wa["branch"], wa["baseRef"])
	}
	if wa["ahead"].(float64) != 1 || wb["ahead"].(float64) != 0 {
		t.Errorf("ahead: a=%v b=%v", wa["ahead"], wb["ahead"])
	}

	files := func(w map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, f := range w["files"].([]any) {
			fm := f.(map[string]any)
			out[fm["path"].(string)] = fm
		}
		return out
	}
	fa, fb := files(wa), files(wb)
	if fa["shared.go"]["status"] != "M" || fa["new.go"]["status"] != "U" || len(fa) != 2 {
		t.Errorf("wt-a files: %v", fa)
	}
	if fb["shared.go"]["status"] != "M" || fb["gone.go"]["status"] != "D" || len(fb) != 2 {
		t.Errorf("wt-b files: %v", fb)
	}
	if ov, _ := fa["shared.go"]["overlap"].([]any); len(ov) != 1 || ov[0] != "wt-b" {
		t.Errorf("wt-a shared.go overlap: %v", fa["shared.go"]["overlap"])
	}
	if _, has := fa["new.go"]["overlap"]; has {
		t.Errorf("new.go must not overlap: %v", fa["new.go"])
	}
}

func TestWorktreeFileDiffAndGutter(t *testing.T) {
	root, _, _ := wtRepo(t)
	s := wtServer(t, root)

	// Committed change on the worktree's branch shows against the merge-base.
	code, m := wtGet(t, s, "/api/diff", url.Values{"path": {"@wt/wt-a/shared.go"}})
	if code != 200 || m["available"] != true || !strings.Contains(m["diff"].(string), "+var A = 2") {
		t.Fatalf("wt-a diff: %d %v", code, m)
	}
	if m["path"] != "@wt/wt-a/shared.go" {
		t.Errorf("virtual path not kept: %v", m["path"])
	}
	// Untracked file diffs as all-new.
	_, m = wtGet(t, s, "/api/diff", url.Values{"path": {"@wt/wt-a/new.go"}})
	if !strings.Contains(m["diff"].(string), "+var N = 1") {
		t.Errorf("untracked diff: %v", m["diff"])
	}
	// Uncommitted change in the other worktree.
	_, m = wtGet(t, s, "/api/diff", url.Values{"path": {"@wt/wt-b/shared.go"}})
	if !strings.Contains(m["diff"].(string), "+var A = 3") {
		t.Errorf("wt-b diff: %v", m["diff"])
	}

	code, m = wtGet(t, s, "/api/file", url.Values{"path": {"@wt/wt-a/shared.go"}})
	if code != 200 || m["total"].(float64) != 4 || m["diffAvailable"] != true || m["worktree"] != "wt-a" {
		t.Fatalf("file: %d %v", code, m)
	}
	code, m = wtGet(t, s, "/api/file", url.Values{"path": {"@wt/wt-b/gone.go"}})
	if code != 200 || m["deleted"] != true {
		t.Errorf("deleted file: %d %v", code, m)
	}
	_, m = wtGet(t, s, "/api/gutter", url.Values{"path": {"@wt/wt-a/new.go"}})
	if added, _ := m["added"].([]any); len(added) != 3 {
		t.Errorf("untracked gutter: %v", m)
	}
	_, m = wtGet(t, s, "/api/gutter", url.Values{"path": {"@wt/wt-a/shared.go"}})
	if mod, _ := m["modified"].([]any); len(mod) != 1 {
		t.Errorf("gutter: %v", m)
	}
}

func TestWorktreePathsCannotEscape(t *testing.T) {
	root, _, _ := wtRepo(t)
	s := wtServer(t, root)
	for _, p := range []string{
		"@wt/wt-a/../platform/shared.go",
		"@wt/wt-a/../../etc/passwd",
		"@wt/nope/shared.go",
		"@wt//etc/passwd",
		"@wt/wt-a//etc/passwd/../../../../etc/passwd",
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, s.routePath("/api/file")+"?path="+url.QueryEscape(p), nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %s", p, rec.Code, rec.Body.String())
		}
	}
	// A normal path in the served root still works.
	if code, _ := wtGet(t, s, "/api/file", url.Values{"path": {"shared.go"}}); code != 200 {
		t.Errorf("root file: %d", code)
	}
}

func TestWorktreesEmptyWithoutGit(t *testing.T) {
	s, _ := newTestServer(t)
	code, m := get(t, s, s.routePath("/api/worktrees"))
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if list, ok := m["worktrees"].([]any); !ok || len(list) != 0 {
		t.Errorf("want empty list, got %v", m["worktrees"])
	}
}

func TestWorktreeFilesAreReadOnly(t *testing.T) {
	root, a, _ := wtRepo(t)
	s := wtServer(t, root)
	h := hostGuard(s)
	before, _ := os.ReadFile(filepath.Join(a, "shared.go"))
	for _, c := range []struct{ path, body string }{
		{"/api/file/save", `{"path":"@wt/wt-a/shared.go","content":"x\n"}`},
		{"/api/fs/new", `{"path":"@wt/wt-a/new2.go","type":"file"}`},
		{"/api/git/stage", `{"path":"@wt/wt-a/shared.go"}`},
	} {
		rec := serve(h, "POST", c.path, "127.0.0.1:7777", "http://127.0.0.1:7777", c.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %s", c.path, rec.Code, rec.Body.String())
		}
	}
	if after, _ := os.ReadFile(filepath.Join(a, "shared.go")); string(after) != string(before) {
		t.Fatal("worktree file changed")
	}
	if _, err := os.Stat(filepath.Join(a, "new2.go")); err == nil {
		t.Fatal("file created in worktree")
	}
}

// A worktree whose folder lost most of its files (an agent's cleaned-up scratch
// copy) is listed as emptied, without its "deleted" files and out of overlaps.
func TestWorktreeEmptiedFolderIsNotAnOverlap(t *testing.T) {
	root, _, _ := wtRepo(t)
	dir := filepath.Dir(root)
	for i := 0; i < wtEmptiedMin; i++ {
		os.WriteFile(filepath.Join(root, "f"+strconv.Itoa(i)+".txt"), []byte("x\n"), 0o644)
	}
	gitTestRun(t, root, "add", "-A")
	gitTestRun(t, root, "commit", "-qm", "many")
	scratch := filepath.Join(dir, "scratch")
	gitTestRun(t, root, "worktree", "add", "-q", "--detach", scratch)
	ents, _ := os.ReadDir(scratch)
	for _, e := range ents {
		if e.Name() != ".git" {
			os.RemoveAll(filepath.Join(scratch, e.Name()))
		}
	}
	s := wtServer(t, root)
	_, m := wtGet(t, s, "/api/worktrees", url.Values{})
	for _, x := range m["worktrees"].([]any) {
		w := x.(map[string]any)
		switch w["name"] {
		case "scratch":
			if w["missing"] == nil || len(w["files"].([]any)) != 0 {
				t.Errorf("scratch: want emptied with no files, got missing=%v files=%d", w["missing"], len(w["files"].([]any)))
			}
		case "wt-a", "wt-b":
			for _, f := range w["files"].([]any) {
				ov, _ := f.(map[string]any)["overlap"].([]any)
				for _, n := range ov {
					if n == "scratch" {
						t.Errorf("%v overlaps the emptied worktree: %v", w["name"], f)
					}
				}
			}
		}
	}
}
