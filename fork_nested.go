package main

// Worktrees inside the repository (Claude Code puts them in .claude/worktrees/)
// are separate checkouts: the Worktrees view shows them, so the main checkout
// leaves them out. Without this they would appear as a second copy of the code
// in the file tree and search, as an untracked change that blocks pull, and
// "stage all" would add them to the next commit as embedded repositories.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// isNestedWorktree reports whether dir is the checkout of a git worktree: its
// .git is a file pointing into some repository's .git/worktrees/. A submodule
// also has a .git file, but it points into .git/modules/, so it stays visible.
func isNestedWorktree(dir string) bool {
	p := filepath.Join(dir, ".git")
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 4096 {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil || !bytes.HasPrefix(b, []byte("gitdir:")) {
		return false
	}
	gitdir := filepath.ToSlash(strings.TrimSpace(string(b[len("gitdir:"):])))
	return strings.Contains(gitdir, "/worktrees/")
}

// nestedWorktreeExcludes returns ":(exclude)<path>" pathspecs for every
// worktree checked out inside root, for `git add -A -- .`.
func nestedWorktreeExcludes(root string) []string {
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	top := evalPath(root)
	var ex []string
	for _, line := range strings.Split(string(out), "\n") {
		p, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
		rel, err := filepath.Rel(top, evalPath(p))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		ex = append(ex, ":(exclude)"+filepath.ToSlash(rel))
	}
	return ex
}

// gitAddAllArgs is `git -C root add -A -- <relpath>`, leaving out nested
// worktrees when the whole tree is staged.
func gitAddAllArgs(root, relpath string) []string {
	args := []string{"-C", root, "add", "-A", "--", relpath}
	if relpath == "." {
		args = append(args, nestedWorktreeExcludes(root)...)
	}
	return args
}

// porcelainHasChanges reads `git status --porcelain` output, ignoring
// untracked entries that are nested worktrees.
func porcelainHasChanges(root, out string) bool {
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if p, ok := strings.CutPrefix(line, "?? "); ok && isNestedWorktree(filepath.Join(root, filepath.FromSlash(p))) {
			continue
		}
		return true
	}
	return false
}
