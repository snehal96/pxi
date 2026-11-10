package main

// Review checkpoint (px0 #90): "mark now", then the explorer flags every file
// that changed since, whether an agent edited it, added it, deleted it or
// committed it.
//
// px0's proposal keeps a size and mtime for every indexed file. pxi keeps one
// git tree hash instead: the working tree as it was, untracked files included,
// written with a throwaway copy of the index (the real index and branch are
// never touched). Comparing that tree with one written now gives the changes,
// by content, so a file that was only saved again does not count. Nothing is
// held per file; the list of changes is worked out when the page asks for it,
// which it does only while a checkpoint is set.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type pxiCheckpointMark struct {
	At   time.Time
	Tree string
}

var (
	pxiCkMu    sync.Mutex
	pxiCkMarks = map[string]pxiCheckpointMark{} // workspace root -> mark; one per open workspace
)

const pxiCkMaxFiles = 2000 // changed paths sent to the page

// pxiSnapshotTree writes the working tree (tracked and untracked, minus
// ignored files) as a git tree and returns its hash. It works on a copy of the
// index, so the stat cache keeps it fast and the user's staging is untouched.
func pxiSnapshotTree(ctx context.Context, root string) (string, error) {
	idx, err := pxiGit(ctx, root, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "pxi-checkpoint-*.index")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if src, err := os.Open(strings.TrimSpace(idx)); err == nil {
		_, err = io.Copy(tmp, src)
		src.Close()
		if err != nil {
			tmp.Close()
			return "", err
		}
	}
	tmp.Close()
	if fi, _ := os.Stat(tmpPath); fi != nil && fi.Size() == 0 {
		os.Remove(tmpPath) // git wants no file, not an empty one, for a fresh index
	}
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+tmpPath, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %v", args[0], err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := run("add", "-A", "--ignore-errors", "--", "."); err != nil {
		return "", err
	}
	return run("write-tree")
}

type pxiCheckpointInfo struct {
	Active    bool              `json:"active"`
	At        string            `json:"at,omitempty"`
	Files     map[string]string `json:"files,omitempty"` // path -> A, M or D
	Count     int               `json:"count"`
	Truncated bool              `json:"truncated,omitempty"`
}

// pxiCheckpointChanges lists what differs between the mark and the disk now.
func pxiCheckpointChanges(ctx context.Context, root string, m pxiCheckpointMark) (pxiCheckpointInfo, error) {
	info := pxiCheckpointInfo{Active: true, At: m.At.Format(time.RFC3339), Files: map[string]string{}}
	now, err := pxiSnapshotTree(ctx, root)
	if err != nil {
		return info, err
	}
	if now == m.Tree {
		return info, nil
	}
	out, err := pxiGit(ctx, root, "diff-tree", "-r", "--no-renames", "--name-status", "-z", m.Tree, now)
	if err != nil {
		return info, err
	}
	parts := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		st := parts[i]
		if st != "A" && st != "D" {
			st = "M"
		}
		info.Count++
		if len(info.Files) < pxiCkMaxFiles {
			info.Files[parts[i+1]] = st
		} else {
			info.Truncated = true
		}
	}
	return info, nil
}

// handlePxiCheckpoint: GET reports the checkpoint and what changed since it;
// POST {"clear": false} marks now, {"clear": true} removes the mark.
func (s *Server) handlePxiCheckpoint(w http.ResponseWriter, r *http.Request) {
	root := s.ix.Root()
	if r.Method == http.MethodPost {
		if !localPost(w, r) {
			return
		}
		var b struct {
			Clear bool `json:"clear"`
		}
		if !pxiReadBody(w, r, &b) {
			return
		}
		if b.Clear {
			pxiCkMu.Lock()
			delete(pxiCkMarks, root)
			pxiCkMu.Unlock()
			writeJSON(w, pxiCheckpointInfo{})
			return
		}
		if !gitAvailable(root) {
			fail(w, http.StatusBadRequest, "a review checkpoint needs a git repository")
			return
		}
		ctx, cancel := pxiCtx(r, 60*time.Second)
		defer cancel()
		tree, err := pxiSnapshotTree(ctx, root)
		if err != nil {
			fail(w, http.StatusInternalServerError, "could not snapshot the working tree: "+err.Error())
			return
		}
		m := pxiCheckpointMark{At: time.Now(), Tree: tree}
		pxiCkMu.Lock()
		pxiCkMarks[root] = m
		pxiCkMu.Unlock()
		writeJSON(w, pxiCheckpointInfo{Active: true, At: m.At.Format(time.RFC3339), Files: map[string]string{}})
		return
	}
	pxiCkMu.Lock()
	m, ok := pxiCkMarks[root]
	pxiCkMu.Unlock()
	if !ok {
		writeJSON(w, pxiCheckpointInfo{})
		return
	}
	ctx, cancel := pxiCtx(r, 60*time.Second)
	defer cancel()
	info, err := pxiCheckpointChanges(ctx, root, m)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, info)
}
