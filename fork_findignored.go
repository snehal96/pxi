package main

// Quick Open also finds files inside gitignored folders (tmp/, notes/, out/),
// which px0 never indexes: the explorer lists them dimmed, but search could
// not find them. Nothing is kept: for a query of three or more characters the
// gitignored folders the index walk met are read on demand, skipping the heavy
// ones (node_modules, build output, virtualenvs, caches), and only paths that
// contain the query's letters in order are scored. The read is capped by
// entries and time. Its list is kept while the user is typing and dropped 15
// seconds after the last search, so nothing stays in memory between searches.
// Matches come after the normal results.

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"px0/internal/fuzzy"
)

const (
	pxiFindIgnoredMinQuery = 3
	pxiFindIgnoredMax      = 20               // ignored matches added to one answer
	pxiFindIgnoredCands    = 2000             // paths scored per query, at most
	pxiFindIgnoredEntries  = 50000            // entries read per walk, at most
	pxiFindIgnoredTime     = time.Second      // wall-clock cap per walk
	pxiFindIgnoredKeep     = 15 * time.Second // the walk's list is dropped this long after the last search
)

// Folders never read: dependencies, build output, environments and caches. They
// hold most of an ignored tree and nothing anyone looks up by name.
var pxiFindIgnoredSkip = map[string]bool{
	"node_modules": true, "bower_components": true, "vendor": true, "Pods": true,
	".git": true, ".hg": true, ".svn": true, ".jj": true,
	"dist": true, "build": true, "out": true, "target": true, ".next": true, ".nuxt": true,
	".output": true, ".svelte-kit": true, ".turbo": true, ".parcel-cache": true, "DerivedData": true,
	".gradle": true, ".terraform": true, "coverage": true, ".nyc_output": true,
	".venv": true, "venv": true, "__pycache__": true, ".pytest_cache": true, ".mypy_cache": true,
	".ruff_cache": true, ".tox": true, ".cache": true, ".idea": true, ".DS_Store": true,
}

// pxiSubseq reports whether every byte of q appears in s in order.
func pxiSubseq(s, q string) bool {
	j := 0
	for i := 0; i < len(s) && j < len(q); i++ {
		if s[i] == q[j] {
			j++
		}
	}
	return j == len(q)
}

// pxiIgnoredRoots lists the gitignored files and folders the index walk met.
func (ix *Index) pxiIgnoredRoots() (dirs, files []string) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	for _, kids := range ix.children {
		for _, k := range kids {
			if !k.Ignored || pxiFindIgnoredSkip[k.Name] {
				continue
			}
			if k.Dir {
				dirs = append(dirs, k.Path)
			} else {
				files = append(files, k.Path)
			}
		}
	}
	return dirs, files
}

// pxiFindIgnored appends matches from gitignored folders to res.
func pxiFindIgnored(ix *Index, query string, res []fuzzy.Result, limit int) []fuzzy.Result {
	q := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(query), " ", ""))
	if len(q) < pxiFindIgnoredMinQuery || !ix.Ready() || len(res) >= limit {
		return res
	}
	if i := strings.IndexAny(q, ":@"); i >= 0 { // file:line and file@symbol
		q = q[:i]
		if len(q) < pxiFindIgnoredMinQuery {
			return res
		}
	}
	var cands []fuzzy.Item
	for _, it := range pxiIgnoredItems(ix) {
		if len(cands) >= pxiFindIgnoredCands {
			break
		}
		if pxiSubseq(it.Lower, q) {
			cands = append(cands, it)
		}
	}
	if len(cands) == 0 {
		return res
	}
	have := make(map[string]bool, len(res))
	for _, r := range res {
		have[r.Path] = true
	}
	n := 0
	for _, r := range fuzzy.Find(cands, q, pxiFindIgnoredMax) {
		if have[r.Path] || len(res) >= limit || n >= pxiFindIgnoredMax {
			continue
		}
		res = append(res, r)
		n++
	}
	return res
}

// The walked list, kept between keystrokes of one search and then freed.
var pxiIgnoredList struct {
	mu    sync.Mutex
	ix    *Index
	items []fuzzy.Item
	timer *time.Timer
}

func pxiIgnoredItems(ix *Index) []fuzzy.Item {
	c := &pxiIgnoredList
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(pxiFindIgnoredKeep, func() {
		c.mu.Lock()
		c.ix, c.items = nil, nil
		c.mu.Unlock()
	})
	if c.ix == ix && c.items != nil {
		return c.items
	}
	c.ix, c.items = ix, pxiWalkIgnored(ix)
	return c.items
}

// pxiWalkIgnored lists the files in gitignored folders, shallow folders first
// so a cap leaves out the deepest trees.
func pxiWalkIgnored(ix *Index) []fuzzy.Item {
	dirs, files := ix.pxiIgnoredRoots()
	sort.Slice(dirs, func(i, j int) bool {
		di, dj := strings.Count(dirs[i], "/"), strings.Count(dirs[j], "/")
		if di != dj {
			return di < dj
		}
		return dirs[i] < dirs[j]
	})
	items := make([]fuzzy.Item, 0, len(files))
	for _, f := range files {
		items = append(items, fuzzy.NewItem(f))
	}
	root := ix.Root()
	deadline := time.Now().Add(pxiFindIgnoredTime)
	seen := 0
	for _, d := range dirs {
		stop := false
		_ = filepath.WalkDir(filepath.Join(root, filepath.FromSlash(d)), func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			seen++
			if seen > pxiFindIgnoredEntries || seen%256 == 0 && time.Now().After(deadline) {
				stop = true
				return filepath.SkipAll
			}
			if e.IsDir() {
				if pxiFindIgnoredSkip[e.Name()] || isNestedWorktree(p) {
					return filepath.SkipDir
				}
				return nil
			}
			if !e.Type().IsRegular() {
				return nil
			}
			if rel, err := filepath.Rel(root, p); err == nil {
				items = append(items, fuzzy.NewItem(filepath.ToSlash(rel)))
			}
			return nil
		})
		if stop {
			break
		}
	}
	return items
}
