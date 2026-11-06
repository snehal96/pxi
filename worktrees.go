package main

// Worktrees: one px0 workspace shows every git worktree of its repository.
//
// A file inside another worktree is addressed by a virtual path,
// "@wt/<name>/<path inside that worktree>", so the existing file, diff and
// gutter endpoints serve it without a second px0 process. Only folders that
// `git worktree list` reports for this repository are reachable this way.
//
// A worktree's changes are measured against its merge-base with the
// repository's base branch (origin/HEAD, else main, else master): commits made
// on the worktree's branch plus everything staged, unstaged and untracked.
// That is "what this ticket changed", whoever made the worktree (Claude Code,
// another agent, or `git worktree add` by hand).
//
// Kept in its own file so upstream px0 changes still merge cleanly.

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"px0/internal/render"
	"px0/internal/table"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const wtPrefix = "@wt/"

// isWTPath reports whether p addresses a file in another worktree.
func isWTPath(p string) bool { return strings.HasPrefix(strings.TrimPrefix(p, "/"), wtPrefix) }

type wtFile struct {
	Path    string   `json:"path"`              // path inside the worktree
	Status  string   `json:"status"`            // A M D (vs base), U untracked
	Overlap []string `json:"overlap,omitempty"` // other worktrees changing the same path
}

type worktree struct {
	Name     string   `json:"name"` // unique; the <name> in @wt/<name>/
	Path     string   `json:"path"`
	Branch   string   `json:"branch,omitempty"` // empty when detached
	Head     string   `json:"head"`
	Base     string   `json:"base,omitempty"` // merge-base commit with BaseRef
	BaseRef  string   `json:"baseRef,omitempty"`
	Ahead    int      `json:"ahead"` // commits since Base
	Dirty    int      `json:"dirty"` // uncommitted files (staged, unstaged, untracked)
	Locked   bool     `json:"locked,omitempty"`
	Prunable bool     `json:"prunable,omitempty"` // folder is gone; `git worktree prune` would drop it
	Files    []wtFile `json:"files"`
	Error    string   `json:"error,omitempty"`
}

type wtRegistry struct {
	root string // the served root; its own worktree is not listed

	mu      sync.Mutex
	list    []*worktree
	byName  map[string]*worktree
	fetched time.Time
}

// wtTTL bounds how stale a listing may be before a request recomputes it.
// Agents change files continuously; two seconds keeps a 10-worktree repo at a
// handful of git calls per second at most.
const wtTTL = 2 * time.Second

func (s *Server) worktrees() *wtRegistry {
	s.wtOnce.Do(func() { s.wt = &wtRegistry{root: s.ix.Root()} })
	return s.wt
}

// Snapshot returns the worktrees, recomputing when older than wtTTL or forced.
func (r *wtRegistry) Snapshot(force bool) []*worktree {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !force && r.list != nil && time.Since(r.fetched) < wtTTL {
		return r.list
	}
	r.list = listWorktrees(r.root)
	r.byName = make(map[string]*worktree, len(r.list))
	for _, w := range r.list {
		r.byName[w.Name] = w
	}
	r.fetched = time.Now()
	return r.list
}

func (r *wtRegistry) lookup(name string) *worktree {
	r.Snapshot(false)
	r.mu.Lock()
	w := r.byName[name]
	r.mu.Unlock()
	if w == nil { // created since the last listing
		r.Snapshot(true)
		r.mu.Lock()
		w = r.byName[name]
		r.mu.Unlock()
	}
	return w
}

// resolve maps "@wt/<name>/<rel>" to the file on disk. It returns the absolute
// path, the cleaned virtual path, the worktree and the path inside it.
func (r *wtRegistry) resolve(p string) (abs, virt string, w *worktree, inner string, ok bool) {
	rest := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(p), "/"), wtPrefix)
	name, sub, _ := strings.Cut(rest, "/")
	if name == "" {
		return "", "", nil, "", false
	}
	w = r.lookup(name)
	if w == nil || w.Prunable {
		return "", "", nil, "", false
	}
	clean := filepath.Clean(filepath.FromSlash(sub))
	if clean == "." {
		clean = ""
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", "", nil, "", false
	}
	abs = filepath.Join(w.Path, clean)
	if abs != w.Path && !strings.HasPrefix(abs, w.Path+string(filepath.Separator)) {
		return "", "", nil, "", false
	}
	if !withinReal(w.Path, abs) { // a symlink inside the worktree must not lead out of it
		return "", "", nil, "", false
	}
	inner = filepath.ToSlash(clean)
	virt = wtPrefix + name
	if inner != "" {
		virt += "/" + inner
	}
	return abs, virt, w, inner, true
}

func gitOut(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return string(out), err
}

// wtBaseRef picks the branch worktrees are compared against.
func wtBaseRef(top string) string {
	if out, err := gitOut(top, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			return ref
		}
	}
	for _, ref := range []string{"main", "master", "origin/main", "origin/master"} {
		if _, err := gitOut(top, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
			return ref
		}
	}
	return ""
}

// listWorktrees lists every worktree of root's repository except root's own,
// with each one's changes measured in parallel.
func listWorktrees(root string) []*worktree {
	if !gitAvailable(root) {
		return []*worktree{}
	}
	topOut, err := gitOut(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return []*worktree{}
	}
	self := evalPath(strings.TrimSpace(topOut))
	out, err := gitOut(root, "worktree", "list", "--porcelain")
	if err != nil {
		return []*worktree{}
	}
	baseRef := wtBaseRef(root)

	var list []*worktree
	var cur *worktree
	flush := func() {
		if cur != nil && evalPath(cur.Path) != self {
			list = append(list, cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &worktree{Path: val, Files: []wtFile{}}
		case "HEAD":
			if cur != nil {
				cur.Head = val
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	flush()

	// Names: the folder's base name, made unique.
	seen := map[string]int{}
	for _, w := range list {
		n := filepath.Base(w.Path)
		seen[n]++
		if seen[n] > 1 {
			n += "-" + strconv.Itoa(seen[n])
		}
		w.Name = n
	}

	var wg sync.WaitGroup
	for _, w := range list {
		if w.Prunable {
			continue
		}
		wg.Add(1)
		go func(w *worktree) {
			defer wg.Done()
			w.BaseRef = baseRef
			fillWorktreeChanges(w)
		}(w)
	}
	wg.Wait()
	markOverlaps(list)
	return list
}

func evalPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// fillWorktreeChanges sets Base, Ahead, Dirty and Files.
func fillWorktreeChanges(w *worktree) {
	base := "HEAD"
	if w.BaseRef != "" {
		if mb, err := gitOut(w.Path, "merge-base", "HEAD", w.BaseRef); err == nil && strings.TrimSpace(mb) != "" {
			base = strings.TrimSpace(mb)
		}
	}
	w.Base = base
	if base != "HEAD" {
		if n, err := gitOut(w.Path, "rev-list", "--count", base+"..HEAD"); err == nil {
			w.Ahead, _ = strconv.Atoi(strings.TrimSpace(n))
		}
	}
	files := map[string]string{}
	if out, err := gitOut(w.Path, "diff", "--name-status", "--no-renames", "-z", base); err == nil {
		parts := strings.Split(out, "\x00")
		for i := 0; i+1 < len(parts); i += 2 {
			files[parts[i+1]] = parts[i][:1]
		}
	} else {
		w.Error = "could not diff against " + base
	}
	if out, err := gitOut(w.Path, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		for _, p := range strings.Split(out, "\x00") {
			if p != "" {
				files[p] = "U"
			}
		}
	}
	if out, err := gitOut(w.Path, "status", "--porcelain", "-z"); err == nil {
		for _, e := range strings.Split(out, "\x00") {
			if len(e) > 3 {
				w.Dirty++
			}
		}
	}
	w.Files = make([]wtFile, 0, len(files))
	for p, st := range files {
		w.Files = append(w.Files, wtFile{Path: p, Status: st})
	}
	sort.Slice(w.Files, func(i, j int) bool { return w.Files[i].Path < w.Files[j].Path })
}

// markOverlaps records, on each changed file, the other worktrees that change
// the same path: those are where parallel tickets will collide.
func markOverlaps(list []*worktree) {
	owners := map[string][]string{}
	for _, w := range list {
		for _, f := range w.Files {
			owners[f.Path] = append(owners[f.Path], w.Name)
		}
	}
	for _, w := range list {
		for i, f := range w.Files {
			if len(owners[f.Path]) < 2 {
				continue
			}
			for _, n := range owners[f.Path] {
				if n != w.Name {
					w.Files[i].Overlap = append(w.Files[i].Overlap, n)
				}
			}
		}
	}
}

func (w *worktree) fileStatus(inner string) string {
	for _, f := range w.Files {
		if f.Path == inner {
			return f.Status
		}
	}
	return ""
}

// wtDiff is the unified diff of one file in a worktree against its base:
// committed, staged, unstaged, and untracked (shown as all-new).
func wtDiff(w *worktree, inner string) string {
	if w.fileStatus(inner) == "U" {
		// --no-index exits 1 when the files differ, which is the normal case.
		cmd := exec.Command("git", "-C", w.Path, "diff", "--no-color", "--no-index", "--", "/dev/null", inner)
		out, _ := cmd.Output()
		return string(out)
	}
	return gitDiffAgainst(w.Path, inner, w.Base)
}

// ---------------------------------------------------------------- HTTP

// handleWorktrees lists the repository's other worktrees and their changes.
// ?refresh=1 skips the short cache.
func (s *Server) handleWorktrees(w http.ResponseWriter, r *http.Request) {
	list := s.worktrees().Snapshot(r.URL.Query().Get("refresh") == "1")
	if list == nil {
		list = []*worktree{}
	}
	writeJSON(w, map[string]any{"root": s.ix.Root(), "worktrees": list})
}

func (s *Server) handleWTDiff(w http.ResponseWriter, r *http.Request) {
	_, virt, wt, inner, ok := s.worktrees().resolve(r.URL.Query().Get("path"))
	if !ok || inner == "" {
		fail(w, 400, "bad path")
		return
	}
	diff := wtDiff(wt, inner)
	writeJSON(w, map[string]any{
		"path":      virt,
		"worktree":  wt.Name,
		"base":      wt.Base,
		"diff":      diff,
		"hunks":     render.HighlightDiff(inner, diff),
		"available": diff != "",
	})
}

func (s *Server) handleWTGutter(w http.ResponseWriter, r *http.Request) {
	_, virt, wt, inner, ok := s.worktrees().resolve(r.URL.Query().Get("path"))
	if !ok || inner == "" {
		fail(w, 400, "bad path")
		return
	}
	var added, modified, deleted []int
	if wt.fileStatus(inner) == "U" {
		if data, err := os.ReadFile(filepath.Join(wt.Path, filepath.FromSlash(inner))); err == nil {
			for i := 1; i <= bytes.Count(data, []byte("\n")); i++ {
				added = append(added, i)
			}
		}
	} else {
		added, modified, deleted = gitHunksAgainst(wt.Path, inner, wt.Base)
	}
	nz := func(v []int) []int {
		if v == nil {
			return []int{}
		}
		return v
	}
	writeJSON(w, map[string]any{
		"path":      virt,
		"available": added != nil || modified != nil || deleted != nil,
		"added":     nz(added),
		"modified":  nz(modified),
		"deleted":   nz(deleted),
	})
}

// handleWTFile serves a worktree file the way handleFile serves one in the
// root, without the index, PR and language-server parts that only apply there.
func (s *Server) handleWTFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	abs, virt, wt, inner, ok := s.worktrees().resolve(q.Get("path"))
	if !ok || inner == "" {
		fail(w, 400, "bad path")
		return
	}
	status := wt.fileStatus(inner)
	st, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) && status == "D" {
			d := render.NewDoc("", inner)
			writeJSON(w, map[string]any{
				"path": virt, "lang": d.Lang, "total": 0, "maxCols": 0,
				"start": 0, "lines": []string{}, "size": 0,
				"exact": true, "refine": false,
				"markdown": render.IsMarkdown(inner), "table": table.IsTable(inner),
				"diffAvailable": true, "deleted": true, "worktree": wt.Name,
			})
			return
		}
		fail(w, 404, err.Error())
		return
	}
	if imageExt[strings.ToLower(filepath.Ext(inner))] {
		writeJSON(w, map[string]any{"path": virt, "image": true, "size": st.Size(), "worktree": wt.Name})
		return
	}
	d, err := render.Open(abs, inner)
	if err != nil {
		fail(w, 415, err.Error())
		return
	}
	start, _ := strconv.Atoi(q.Get("start"))
	count, _ := strconv.Atoi(q.Get("count"))
	if count <= 0 {
		count = render.ChunkLines
	}
	start = max(0, min(start, d.Total))
	lines, exact := d.Lines(start, start+count)
	_, coming := d.Exact()
	resp := map[string]any{
		"path": virt, "lang": d.Lang, "total": d.Total, "maxCols": d.MaxCols,
		"start": start, "lines": lines, "size": st.Size(),
		"exact": exact, "refine": !exact && coming,
		"markdown": render.IsMarkdown(inner), "table": table.IsTable(inner),
		"diffAvailable": status != "",
		"worktree":      wt.Name,
	}
	if q.Get("raw") == "1" {
		raw := make([]string, len(lines))
		for i := range lines {
			raw[i] = d.Raw(start + i + 1)
		}
		resp["raw"] = raw
	}
	writeJSON(w, resp)
}
