package main

// The explorer's right-click menu can ignore or stop ignoring a file or folder:
//
//   - "add" appends it to the workspace's .gitignore (shared once committed);
//   - "local" appends it to the repository's info/exclude, which git reads the
//     same way but never commits or pushes: ignored on this Mac only;
//   - "remove" deletes the rule that ignores it, but only when that rule names
//     exactly this path. A wider pattern (tmp/, *.log) would un-ignore other
//     files too, so then the rule is returned for the user to edit.
//
// Only .gitignore files inside the workspace and the repository's own
// info/exclude are ever written; never a global excludes file, and never
// through a symlink.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type pxiIgnoreRule struct {
	File    string `json:"file"`              // workspace-relative, or "" for one outside it
	Abs     string `json:"abs"`               // where the rule lives
	Line    int    `json:"line"`              // 1-based
	Pattern string `json:"pattern"`           // the rule as written
	Local   bool   `json:"local,omitempty"`   // in info/exclude, not shared
	Outside bool   `json:"outside,omitempty"` // a global excludes file pxi does not edit
}

type pxiIgnoreResult struct {
	Ignored bool           `json:"ignored"`           // ignored after the change
	Tracked bool           `json:"tracked,omitempty"` // committed, so git keeps tracking it regardless
	Rule    *pxiIgnoreRule `json:"rule,omitempty"`    // the rule that (still) ignores it
	Changed string         `json:"changed,omitempty"` // the file written
}

// pxiIgnoreEscape writes rel as a .gitignore pattern matching only that path.
func pxiIgnoreEscape(rel string, dir bool) string {
	var b strings.Builder
	b.WriteByte('/')
	for i := 0; i < len(rel); i++ {
		switch c := rel[i]; c {
		case '\\', '*', '?', '[', '!', '#':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	s := b.String()
	if strings.HasSuffix(s, " ") {
		s = s[:len(s)-1] + "\\ "
	}
	if dir {
		s += "/"
	}
	return s
}

// pxiIgnoreUnescape reverses pxiIgnoreEscape's escaping on a pattern.
func pxiIgnoreUnescape(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' && i+1 < len(p) {
			i++
		}
		b.WriteByte(p[i])
	}
	return b.String()
}

// pxiIgnoreExact reports whether pattern, from the ignore file in base (a
// repo-relative folder, "" for the top), names exactly the repo-relative path.
func pxiIgnoreExact(pattern, base, repoRel string, dir bool) bool {
	if strings.HasPrefix(pattern, "!") {
		return false
	}
	for i := 0; i < len(pattern); i++ { // an unescaped wildcard matches more than one path
		if pattern[i] == '\\' {
			i++
		} else if strings.IndexByte("*?[", pattern[i]) >= 0 {
			return false
		}
	}
	p := pxiIgnoreUnescape(pattern)
	if strings.HasSuffix(p, "/") {
		if !dir {
			return false
		}
		p = strings.TrimSuffix(p, "/")
	}
	rel := repoRel
	if base != "" {
		if !strings.HasPrefix(repoRel, base+"/") {
			return false
		}
		rel = strings.TrimPrefix(repoRel, base+"/")
	}
	if strings.HasPrefix(p, "/") {
		return strings.TrimPrefix(p, "/") == rel
	}
	if strings.Contains(p, "/") { // anchored to base by its middle slash
		return p == rel
	}
	return p == rel // a bare name matches at any depth; exact only at base
}

func pxiGitOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\n"), err
}

// pxiCheckIgnore returns the rule ignoring repoRel, or nil.
func pxiCheckIgnore(ctx context.Context, top, repoRel string, dir bool) *pxiIgnoreRule {
	p := repoRel
	if dir {
		p += "/"
	}
	out, err := pxiGitOut(ctx, top, "check-ignore", "-v", "--no-index", "--", p)
	if err != nil || out == "" {
		return nil // exit 1: not ignored
	}
	meta, _, ok := strings.Cut(out, "\t")
	if !ok {
		return nil
	}
	// source:line:pattern; the source may itself contain ':'.
	i := strings.LastIndex(meta, ":")
	if i < 0 {
		return nil
	}
	pattern := meta[i+1:]
	rest := meta[:i]
	j := strings.LastIndex(rest, ":")
	if j < 0 {
		return nil
	}
	line, _ := strconv.Atoi(rest[j+1:])
	src := rest[:j]
	if !filepath.IsAbs(src) {
		src = filepath.Join(top, filepath.FromSlash(src))
	}
	r := &pxiIgnoreRule{Abs: src, Line: line, Pattern: pattern}
	if line <= 0 && pattern == "" {
		return nil
	}
	return r
}

// pxiAppendRule appends line to the ignore file at abs, creating it.
func pxiAppendRule(abs, line string) error {
	if fi, err := os.Lstat(abs); err == nil && !fi.Mode().IsRegular() {
		return errors.New(filepath.Base(abs) + " is not a regular file")
	}
	b, err := os.ReadFile(abs)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimRight(l, "\r") == line {
			return nil // already there
		}
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	b = append(b, line...)
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, b, 0o644)
}

// pxiDeleteRuleLine removes line n (1-based) of abs if it still reads pattern.
func pxiDeleteRuleLine(abs string, n int, pattern string) error {
	if fi, err := os.Lstat(abs); err != nil || !fi.Mode().IsRegular() {
		return errors.New("cannot edit " + filepath.Base(abs))
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(b), "\n")
	if n < 1 || n > len(lines) || strings.TrimSpace(lines[n-1]) != strings.TrimSpace(pattern) {
		return errors.New(filepath.Base(abs) + " changed; try again")
	}
	lines = append(lines[:n-1], lines[n:]...)
	return os.WriteFile(abs, []byte(strings.Join(lines, "")), 0o644)
}

func (s *Server) handlePxiGitignore(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var b struct {
		Path   string `json:"path"`
		Action string `json:"action"` // "check", "add", "local", "remove"
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	root := s.ix.Root()
	if isWTPath(b.Path) || pxiIsRemotePath(b.Path) || !gitAvailable(root) {
		fail(w, http.StatusBadRequest, "only files in this repository can be ignored here")
		return
	}
	abs, rel, ok := s.resolvePath(b.Path)
	if !ok || rel == "" || rel == "." {
		fail(w, http.StatusBadRequest, "bad path")
		return
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		fail(w, http.StatusNotFound, "no such file")
		return
	}
	dir := fi.IsDir()
	ctx, cancel := pxiCtx(r, 15*time.Second)
	defer cancel()
	top, err := pxiGitOut(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		fail(w, http.StatusBadRequest, "not a git repository")
		return
	}
	if t, err := filepath.EvalSymlinks(top); err == nil {
		top = t
	}
	rootReal := root
	if t, err := filepath.EvalSymlinks(root); err == nil {
		rootReal = t
	}
	prefix, _ := filepath.Rel(top, rootReal)
	prefix = filepath.ToSlash(prefix)
	if prefix == "." {
		prefix = ""
	}
	repoRel := filepath.ToSlash(rel)
	if prefix != "" {
		repoRel = prefix + "/" + repoRel
	}
	exclude, _ := pxiGitOut(ctx, root, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if exclude != "" {
		if d, err := filepath.EvalSymlinks(filepath.Dir(exclude)); err == nil {
			exclude = filepath.Join(d, filepath.Base(exclude))
		}
	}

	changed := ""
	switch b.Action {
	case "check":
	case "add":
		changed = filepath.Join(root, ".gitignore")
		err = pxiAppendRule(changed, pxiIgnoreEscape(filepath.ToSlash(rel), dir))
	case "local":
		if exclude == "" {
			err = errors.New("cannot find this repository's info/exclude")
			break
		}
		changed = exclude
		err = pxiAppendRule(exclude, pxiIgnoreEscape(repoRel, dir))
	case "remove":
		rule := pxiCheckIgnore(ctx, top, repoRel, dir)
		if rule == nil {
			break
		}
		base := ""
		inRepo := false
		if rule.Abs == exclude {
			inRepo = true
		} else if rr, e := filepath.Rel(top, rule.Abs); e == nil && !strings.HasPrefix(rr, "..") && filepath.Base(rule.Abs) == ".gitignore" {
			inRepo = true
			base = filepath.ToSlash(filepath.Dir(rr))
			if base == "." {
				base = ""
			}
		}
		if inRepo && pxiIgnoreExact(rule.Pattern, base, repoRel, dir) {
			changed = rule.Abs
			err = pxiDeleteRuleLine(rule.Abs, rule.Line, rule.Pattern)
		}
	default:
		fail(w, http.StatusBadRequest, "unknown action")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	res := pxiIgnoreResult{Changed: changed}
	if rule := pxiCheckIgnore(ctx, top, repoRel, dir); rule != nil {
		res.Ignored = true
		rule.Local = rule.Abs == exclude
		if fr, e := filepath.Rel(rootReal, rule.Abs); e == nil && !strings.HasPrefix(fr, "..") {
			rule.File = filepath.ToSlash(fr)
		} else if !rule.Local {
			rule.Outside = true
		}
		res.Rule = rule
	}
	if out, _ := pxiGitOut(ctx, top, "ls-files", "--", repoRel); out != "" {
		res.Tracked = true
	}
	if changed != "" {
		s.ix.Build() // the ignored set changed: re-walk so the tree and search agree
		if s.gitWatcher != nil {
			s.gitWatcher.Trigger()
		}
	}
	writeJSON(w, res)
}
