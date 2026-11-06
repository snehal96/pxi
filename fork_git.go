package main

// pxi Source Control sections: stashes, branches, remotes and tags. History
// reuses /api/git/log and /api/commitfiles; a stash is a commit whose first
// parent is HEAD at the time, so its files and diffs use the same endpoints
// (/api/commitfiles?sha=, /api/file and /api/diff with ref=).
// The UI is web/src/pxi-scm.js. Mutations are POSTs checked by localPost.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (s *Server) registerPxiGitRoutes() {
	s.mux.HandleFunc(s.routePath("/api/pxi/stashes"), s.handlePxiStashes)
	s.mux.HandleFunc(s.routePath("/api/pxi/stash"), s.handlePxiStash)
	s.mux.HandleFunc(s.routePath("/api/pxi/stash-files"), s.handlePxiStashFiles)
	s.mux.HandleFunc(s.routePath("/api/pxi/branches"), s.handlePxiBranches)
	s.mux.HandleFunc(s.routePath("/api/pxi/branch"), s.handlePxiBranch)
	s.mux.HandleFunc(s.routePath("/api/pxi/remotes"), s.handlePxiRemotes)
	s.mux.HandleFunc(s.routePath("/api/pxi/fetch"), s.handlePxiFetch)
}

// pxiGit runs git in the workspace with no terminal prompts, so a fetch that
// needs credentials fails instead of hanging.
func pxiGit(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return string(out), fmt.Errorf("%s", msg)
	}
	return string(out), nil
}

func pxiCtx(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

// pxiAfterGitChange refreshes the file tree and status after a git operation.
func (s *Server) pxiAfterGitChange(w http.ResponseWriter, extra map[string]any) {
	resp := map[string]any{"ok": true}
	for k, v := range extra {
		resp[k] = v
	}
	if s.gitWatcher != nil {
		resp["git"] = s.gitWatcher.Refresh()
	} else {
		s.ix.UpdateGitStatus()
	}
	writeJSON(w, resp)
}

func pxiReadBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return false
	}
	return true
}

// ---------------------------------------------------------------- stashes

type pxiStash struct {
	Ref     string `json:"ref"` // stash@{n}
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Branch  string `json:"branch,omitempty"`
	Date    string `json:"date"`
}

var stashSubject = regexp.MustCompile(`^(?:WIP on|On) ([^:]+): (.*)$`)

func (s *Server) handlePxiStashes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := pxiCtx(r, 10*time.Second)
	defer cancel()
	list := []pxiStash{}
	if gitAvailable(s.ix.Root()) {
		out, err := pxiGit(ctx, s.ix.Root(), "stash", "list", "--format=%gd%x1f%H%x1f%gs%x1f%cr")
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				f := strings.Split(line, "\x1f")
				if len(f) != 4 {
					continue
				}
				st := pxiStash{Ref: f[0], SHA: f[1], Message: f[2], Date: f[3]}
				if m := stashSubject.FindStringSubmatch(f[2]); m != nil {
					st.Branch, st.Message = m[1], m[2]
				}
				list = append(list, st)
			}
		}
	}
	writeJSON(w, map[string]any{"stashes": list})
}

// handlePxiStashFiles lists what a stash holds. A stash commit has two or
// three parents (HEAD, the index, and the untracked files with -u), so the
// generic commit view would mix them: tracked changes are diffed against the
// first parent here, and untracked files come from the third parent's tree.
// The UI opens each with /api/diff?ref=<sha>&base=<base>.
func (s *Server) handlePxiStashFiles(w http.ResponseWriter, r *http.Request) {
	sha := r.URL.Query().Get("sha")
	if !validSHA(sha) {
		fail(w, http.StatusBadRequest, "bad sha")
		return
	}
	ctx, cancel := pxiCtx(r, 10*time.Second)
	defer cancel()
	root := s.ix.Root()
	base, err := pxiGit(ctx, root, "rev-parse", "--verify", "--quiet", sha+"^1")
	if err != nil {
		fail(w, http.StatusNotFound, "not a stash")
		return
	}
	base = strings.TrimSpace(base)
	type file struct {
		Path   string `json:"path"`
		Status string `json:"status"`
		Ref    string `json:"ref"`
		Base   string `json:"base"`
	}
	files := []file{}
	if out, err := pxiGit(ctx, root, "diff", "--name-status", "--no-renames", "-z", base, sha); err == nil {
		parts := strings.Split(out, "\x00")
		for i := 0; i+1 < len(parts); i += 2 {
			files = append(files, file{Path: parts[i+1], Status: parts[i][:1], Ref: sha, Base: base})
		}
	}
	if u, err := pxiGit(ctx, root, "rev-parse", "--verify", "--quiet", sha+"^3"); err == nil {
		u = strings.TrimSpace(u)
		if out, err := pxiGit(ctx, root, "ls-tree", "-r", "-z", "--name-only", u); err == nil {
			for _, p := range strings.Split(out, "\x00") {
				if p != "" {
					files = append(files, file{Path: p, Status: "U", Ref: u, Base: emptyTreeSHA})
				}
			}
		}
	}
	writeJSON(w, map[string]any{"sha": sha, "base": base, "files": files})
}

// git's empty tree: what an untracked file in a stash is compared against.
const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

var stashRef = regexp.MustCompile(`^stash@\{\d+\}$`)

// handlePxiStash: {"action": "push"|"apply"|"pop"|"drop", "ref", "message", "untracked"}.
func (s *Server) handlePxiStash(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var b struct {
		Action    string `json:"action"`
		Ref       string `json:"ref"`
		Message   string `json:"message"`
		Untracked bool   `json:"untracked"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	root := s.ix.Root()
	var args []string
	switch b.Action {
	case "push":
		args = []string{"stash", "push"}
		if msg := strings.TrimSpace(b.Message); msg != "" {
			args = append(args, "-m", msg)
		}
		if b.Untracked {
			// Untracked files too, but never a worktree checked out inside
			// the repo (fork_nested.go): it would be stashed as a repository.
			args = append(args, "--include-untracked", "--", ".")
			args = append(args, nestedWorktreeExcludes(root)...)
		}
	case "apply", "pop", "drop":
		if !stashRef.MatchString(b.Ref) {
			fail(w, http.StatusBadRequest, "bad stash")
			return
		}
		args = []string{"stash", b.Action, b.Ref}
	default:
		fail(w, http.StatusBadRequest, "unknown action")
		return
	}
	ctx, cancel := pxiCtx(r, 30*time.Second)
	defer cancel()
	out, err := pxiGit(ctx, root, args...)
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	s.pxiAfterGitChange(w, map[string]any{"output": strings.TrimSpace(out)})
}

// ---------------------------------------------------------------- branches

type pxiBranch struct {
	Name     string `json:"name"`
	Current  bool   `json:"current,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
	Gone     bool   `json:"gone,omitempty"` // upstream deleted
	Date     string `json:"date"`
	Subject  string `json:"subject"`
	SHA      string `json:"sha"`
	Worktree string `json:"worktree,omitempty"` // checked out in another worktree
}

var trackRe = regexp.MustCompile(`(ahead|behind) (\d+)`)

func (s *Server) handlePxiBranches(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := pxiCtx(r, 10*time.Second)
	defer cancel()
	root := s.ix.Root()
	local, remote := []pxiBranch{}, []pxiBranch{}
	if gitAvailable(root) {
		top := ""
		if out, err := pxiGit(ctx, root, "rev-parse", "--show-toplevel"); err == nil {
			top = evalPath(strings.TrimSpace(out))
		}
		format := "--format=%(refname)%1f%(refname:short)%1f%(HEAD)%1f%(upstream:short)%1f%(upstream:track)%1f%(committerdate:relative)%1f%(subject)%1f%(objectname)%1f%(worktreepath)"
		out, err := pxiGit(ctx, root, "for-each-ref", "--sort=-committerdate", format, "refs/heads", "refs/remotes")
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				f := strings.Split(line, "\x1f")
				if len(f) != 9 {
					continue
				}
				b := pxiBranch{Name: f[1], Current: f[2] == "*", Upstream: f[3], Date: f[5], Subject: f[6], SHA: f[7]}
				for _, m := range trackRe.FindAllStringSubmatch(f[4], -1) {
					n, _ := strconv.Atoi(m[2])
					if m[1] == "ahead" {
						b.Ahead = n
					} else {
						b.Behind = n
					}
				}
				b.Gone = strings.Contains(f[4], "gone")
				if wt := f[8]; wt != "" && !b.Current && evalPath(wt) != top {
					b.Worktree = wt
				}
				if strings.HasPrefix(f[0], "refs/heads/") {
					local = append(local, b)
				} else if !strings.HasSuffix(f[0], "/HEAD") {
					remote = append(remote, b)
				}
			}
		}
	}
	writeJSON(w, map[string]any{"local": local, "remote": remote})
}

// handlePxiBranch: {"action": "switch"|"create"|"delete"|"track", "name"}.
// track checks out a remote branch ("origin/x") as a local branch following it.
func (s *Server) handlePxiBranch(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var b struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	root := s.ix.Root()
	ctx, cancel := pxiCtx(r, 30*time.Second)
	defer cancel()
	name := strings.TrimSpace(b.Name)
	if name == "" || strings.HasPrefix(name, "-") {
		fail(w, http.StatusBadRequest, "bad branch name")
		return
	}
	var args []string
	switch b.Action {
	case "switch":
		args = []string{"switch", name}
	case "create":
		if _, err := pxiGit(ctx, root, "check-ref-format", "--branch", name); err != nil {
			fail(w, http.StatusBadRequest, "not a valid branch name")
			return
		}
		args = []string{"switch", "-c", name}
	case "delete":
		args = []string{"branch", "-d", "--", name} // -d: refuses to drop unmerged work
	case "track":
		args = []string{"switch", "--track", name}
	default:
		fail(w, http.StatusBadRequest, "unknown action")
		return
	}
	// Names starting with "-" were rejected above, so none is read as an option.
	if _, err := pxiGit(ctx, root, args...); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	s.pxiAfterGitChange(w, nil)
}

// ---------------------------------------------------------------- remotes and tags

type pxiRemote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type pxiTag struct {
	Name    string `json:"name"`
	SHA     string `json:"sha"` // the commit it points at
	Date    string `json:"date"`
	Subject string `json:"subject"`
}

func (s *Server) handlePxiRemotes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := pxiCtx(r, 10*time.Second)
	defer cancel()
	root := s.ix.Root()
	remotes, tags := []pxiRemote{}, []pxiTag{}
	if gitAvailable(root) {
		if out, err := pxiGit(ctx, root, "remote", "-v"); err == nil {
			seen := map[string]bool{}
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				f := strings.Fields(line)
				if len(f) >= 2 && !seen[f[0]] {
					seen[f[0]] = true
					remotes = append(remotes, pxiRemote{Name: f[0], URL: f[1]})
				}
			}
		}
		format := "--format=%(refname:short)%1f%(*objectname)%1f%(objectname)%1f%(creatordate:relative)%1f%(subject)"
		if out, err := pxiGit(ctx, root, "for-each-ref", "--sort=-creatordate", "--count=100", format, "refs/tags"); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				f := strings.Split(line, "\x1f")
				if len(f) != 5 {
					continue
				}
				sha := f[1] // annotated: the tagged commit
				if sha == "" {
					sha = f[2]
				}
				tags = append(tags, pxiTag{Name: f[0], SHA: sha, Date: f[3], Subject: f[4]})
			}
		}
	}
	writeJSON(w, map[string]any{"remotes": remotes, "tags": tags})
}

// handlePxiFetch: {"remote": name} or {} for all remotes.
func (s *Server) handlePxiFetch(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var b struct {
		Remote string `json:"remote"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	args := []string{"fetch", "--prune", "--all"}
	if b.Remote != "" {
		if strings.HasPrefix(b.Remote, "-") {
			fail(w, http.StatusBadRequest, "bad remote")
			return
		}
		args = []string{"fetch", "--prune", b.Remote}
	}
	ctx, cancel := pxiCtx(r, 2*time.Minute)
	defer cancel()
	if _, err := pxiGit(ctx, s.ix.Root(), args...); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.pxiAfterGitChange(w, nil)
}
