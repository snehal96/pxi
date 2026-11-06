package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A repository with a Claude Code-style worktree in .claude/worktrees/fix.
func nestedRepo(t *testing.T) string {
	t.Helper()
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644)
	gitTestRun(t, root, "init", "-q", "-b", "main")
	gitTestRun(t, root, "config", "user.email", "t@example.com")
	gitTestRun(t, root, "config", "user.name", "T")
	gitTestRun(t, root, "config", "commit.gpgsign", "false")
	gitTestRun(t, root, "add", "-A")
	gitTestRun(t, root, "commit", "-qm", "init")
	gitTestRun(t, root, "worktree", "add", "-q", "-b", "fix", ".claude/worktrees/fix")
	os.WriteFile(filepath.Join(root, ".claude/worktrees/fix/main.go"), []byte("package main // fixed\n"), 0o644)
	return root
}

func TestNestedWorktreesStayOutOfTheMainCheckout(t *testing.T) {
	root := nestedRepo(t)

	if !isNestedWorktree(filepath.Join(root, ".claude/worktrees/fix")) {
		t.Fatal("worktree not recognised")
	}

	for p := range gitStatus(root) {
		if strings.HasPrefix(p, ".claude") {
			t.Errorf("git status lists nested worktree: %s", p)
		}
	}
	if gitHasUncommittedChanges(root) {
		t.Error("a nested worktree alone must not count as uncommitted changes (it blocks pull)")
	}

	os.WriteFile(filepath.Join(root, "new.go"), []byte("package main\n"), 0o644)
	if err := gitStage(root, ""); err != nil {
		t.Fatal(err)
	}
	staged := gitTestRun(t, root, "diff", "--cached", "--name-only")
	if strings.Contains(staged, ".claude") {
		t.Fatalf("stage all added the nested worktree:\n%s", staged)
	}
	if !strings.Contains(staged, "new.go") {
		t.Fatalf("stage all missed new.go:\n%s", staged)
	}
	// A submodule also has a .git file, but it points into .git/modules.
	sub := filepath.Join(root, "libs", "lib")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../../.git/modules/lib\n"), 0o644)
	os.WriteFile(filepath.Join(sub, "lib.go"), []byte("package lib\n"), 0o644)
	if isNestedWorktree(sub) {
		t.Fatal("submodule-style folder taken for a worktree")
	}

	ix := NewIndex(root)
	ix.Build()
	files := ix.Files()
	sawLib := false
	for _, f := range files {
		if strings.HasPrefix(f.Path, ".claude/worktrees/") {
			t.Fatalf("indexed a file of a nested worktree: %s", f.Path)
		}
		sawLib = sawLib || f.Path == "libs/lib/lib.go"
	}
	if !sawLib {
		t.Error("submodule-style folder must stay indexed")
	}
}
