package main

// Files created outside pxi (by an agent in a terminal, a script, the Finder)
// join the explorer and Quick Open without a reindex. px0 only adds a file when
// pxi itself writes it; git status, which the git watcher already polls while a
// page is open, lists every new untracked file, so a file it reports that the
// index lacks is added, and an untracked file that vanished from it and from
// disk is removed. No watcher, no extra polling: this runs only on a status
// change. Gitignored files and folders that are not git repositories still need
// Re-index Workspace.

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// The untracked paths seen on the last change, per index, so a deleted one can
// be dropped. Bounded by what git status reports.
var pxiUntrackedSeen sync.Map // *Index -> map[string]struct{}

func pxiSyncNewFiles(ix *Index, statuses map[string]string) {
	if !ix.Ready() {
		return
	}
	files := ix.Files()
	has := func(p string) bool {
		i := sort.Search(len(files), func(i int) bool { return files[i].Path >= p })
		return i < len(files) && files[i].Path == p
	}
	now := make(map[string]struct{})
	var add []string
	for p, code := range statuses {
		if code != "U" && code != "A" {
			continue
		}
		now[p] = struct{}{}
		if !has(p) {
			add = append(add, p)
		}
	}
	var drop []string
	if v, ok := pxiUntrackedSeen.Load(ix); ok {
		for p := range v.(map[string]struct{}) {
			if _, still := now[p]; still || statuses[p] != "" {
				continue
			}
			if _, err := os.Lstat(filepath.Join(ix.Root(), filepath.FromSlash(p))); os.IsNotExist(err) {
				drop = append(drop, p)
			}
		}
	}
	pxiUntrackedSeen.Store(ix, now)
	for _, p := range add {
		ix.TouchPath(p)
	}
	for _, p := range drop {
		ix.TouchPath(p)
	}
}
