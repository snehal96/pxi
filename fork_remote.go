//go:build !windows

package main

// Remote (SSH) file view: browse a folder on another machine and read its files,
// read-only. Remotes are saved (settings dir, remotes.json) so they come back
// after a restart. Every command goes through one shared ssh connection per
// host (ControlMaster), kept for 30 minutes after last use and pinged so a
// dead link is noticed; a dropped connection is opened again on the next
// request, so the view reconnects on its own. Nothing is installed on the
// remote: listing is `ls`, reading is `head -c`.
//
// Files are addressed as "@ssh/<id>/<path inside the folder>" so px0's
// viewer opens them like any other file (handleFile hook in server.go).
// ssh runs with BatchMode: keys or an agent, never a password prompt.

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"px0/internal/render"
	"px0/internal/settings"
	"px0/internal/table"
)

const (
	pxiRemotePrefix   = "@ssh/"
	pxiRemoteMax      = 20      // saved remotes
	pxiRemoteMaxList  = 3000    // entries listed per folder
	pxiRemoteMaxBytes = 2 << 20 // largest file opened
)

type pxiSSHRemote struct {
	ID   string `json:"id"`
	Host string `json:"host"`
	Path string `json:"path"` // absolute on the remote
}

var (
	pxiRemoteMu  sync.Mutex
	pxiRemoteDoc struct { // the last file opened, for its later chunks; dropped after a minute
		key   string
		doc   *render.Doc
		size  int
		timer *time.Timer
	}
	pxiHostRe = regexp.MustCompile(`^[A-Za-z0-9_.@:\[\]%+-]{1,253}$`)
)

func pxiIsRemotePath(p string) bool {
	return strings.HasPrefix(strings.TrimPrefix(p, "/"), pxiRemotePrefix)
}

func pxiRemotesPath() string {
	return filepath.Join(filepath.Dir(settings.Path()), "remotes.json")
}

func pxiRemotesRead() []pxiSSHRemote {
	var list []pxiSSHRemote
	if b, err := os.ReadFile(pxiRemotesPath()); err == nil {
		json.Unmarshal(b, &list)
	}
	return list
}

func pxiRemotesWrite(list []pxiSSHRemote) error {
	b, _ := json.MarshalIndent(list, "", "  ")
	os.MkdirAll(filepath.Dir(pxiRemotesPath()), 0o700)
	return os.WriteFile(pxiRemotesPath(), b, 0o600)
}

func pxiRemoteByID(id string) (pxiSSHRemote, bool) {
	for _, r := range pxiRemotesRead() {
		if r.ID == id {
			return r, true
		}
	}
	return pxiSSHRemote{}, false
}

// pxiShQuote quotes s for the remote POSIX shell.
func pxiShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pxiControlDir is a short private folder for the ssh control sockets (their
// paths must fit in 104 bytes, which the settings folder may not).
func pxiControlDir() (string, error) {
	dir := fmt.Sprintf("/tmp/pxi-ssh-%d", os.Getuid())
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm() != 0o700 || !pxiOwnedByMe(fi) {
		return "", fmt.Errorf("%s is not a private folder of this user", dir)
	}
	return dir, nil
}

// pxiSSH runs one command on host over the shared connection.
func pxiSSH(ctx context.Context, host, command string) ([]byte, error) {
	if !pxiHostRe.MatchString(host) || strings.HasPrefix(host, "-") {
		return nil, fmt.Errorf("not a host name: %q", host)
	}
	dir, err := pxiControlDir()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath="+dir+"/%C",
		"-o", "ControlPersist=30m",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"--", host, command)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Stdin = nil
	// The shared connection ssh leaves running in the background can keep
	// these pipes open; return once this command itself has exited.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		switch {
		case strings.Contains(msg, "Host key verification failed"):
			msg += ". Connect once in the terminal (ssh " + host + ") to accept the host key."
		case strings.Contains(msg, "Permission denied"):
			msg += ". pxi connects with your SSH keys or agent only, not a password."
		}
		return out.Bytes(), fmt.Errorf("%s", msg)
	}
	return out.Bytes(), nil
}

// pxiRemoteCd is a shell `cd` to p, keeping ~ expandable.
func pxiRemoteCd(p string) string {
	switch {
	case p == "" || p == "~":
		return "cd"
	case strings.HasPrefix(p, "~/"):
		return "cd ~/" + pxiShQuote(p[2:])
	default:
		return "cd -- " + pxiShQuote(p)
	}
}

// pxiRemoteRel cleans a path inside a remote folder; false if it leaves it.
func pxiRemoteRel(rel string) (string, bool) {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return "", true
	}
	c := path.Clean(rel)
	if c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, "/") {
		return "", false
	}
	return c, true
}

func pxiRemoteLocal(w http.ResponseWriter, r *http.Request) bool {
	if !isLoopbackHost(r.RemoteAddr) || !isLoopbackHost(r.Host) {
		fail(w, http.StatusForbidden, "remote files are only available on this machine")
		return false
	}
	return true
}

// handlePxiSSHRemotes: GET lists saved remotes and the hosts in ~/.ssh/config;
// POST {"host","path"} connects and saves, {"remove": id} forgets one.
func (s *Server) handlePxiSSHRemotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, map[string]any{"remotes": pxiRemotesRead(), "hosts": pxiSSHConfigHosts()})
		return
	}
	var b struct {
		Host   string `json:"host"`
		Path   string `json:"path"`
		Remove string `json:"remove"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	pxiRemoteMu.Lock()
	defer pxiRemoteMu.Unlock()
	list := pxiRemotesRead()
	if b.Remove != "" {
		out := list[:0]
		for _, x := range list {
			if x.ID != b.Remove {
				out = append(out, x)
			}
		}
		pxiRemotesWrite(out)
		writeJSON(w, map[string]any{"remotes": out})
		return
	}
	host := strings.TrimSpace(b.Host)
	ctx, cancel := pxiCtx(r, 30*time.Second)
	defer cancel()
	out, err := pxiSSH(ctx, host, pxiRemoteCd(strings.TrimSpace(b.Path))+" && pwd")
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	abs := strings.TrimSpace(string(out))
	if i := strings.LastIndexByte(abs, '\n'); i >= 0 { // login banners print first
		abs = abs[i+1:]
	}
	if !strings.HasPrefix(abs, "/") {
		fail(w, http.StatusBadGateway, "unexpected answer from "+host)
		return
	}
	sum := sha1.Sum([]byte(host + "\x00" + abs))
	rem := pxiSSHRemote{ID: hex.EncodeToString(sum[:])[:8], Host: host, Path: abs}
	found := false
	for _, x := range list {
		if x.ID == rem.ID {
			found = true
		}
	}
	if !found {
		if len(list) >= pxiRemoteMax {
			fail(w, http.StatusBadRequest, "too many saved remotes; remove one first")
			return
		}
		list = append(list, rem)
		if err := pxiRemotesWrite(list); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, map[string]any{"remote": rem, "remotes": list})
}

type pxiRemoteEntry struct {
	Name string `json:"name"`
	Path string `json:"path"` // inside the remote folder
	Dir  bool   `json:"dir,omitempty"`
}

// handlePxiRemoteList: GET ?id=&dir= lists one folder, folders first.
func (s *Server) handlePxiRemoteList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rem, ok := pxiRemoteByID(q.Get("id"))
	if !ok {
		fail(w, http.StatusNotFound, "unknown remote")
		return
	}
	dir, ok := pxiRemoteRel(q.Get("dir"))
	if !ok {
		fail(w, http.StatusBadRequest, "bad path")
		return
	}
	target := rem.Path
	if dir != "" {
		target += "/" + dir
	}
	ctx, cancel := pxiCtx(r, 30*time.Second)
	defer cancel()
	out, err := pxiSSH(ctx, rem.Host, "cd -- "+pxiShQuote(target)+" && LC_ALL=C ls -1Ap")
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	entries := []pxiRemoteEntry{}
	more := false
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" || line == "./" || line == "../" || line == ".git/" {
			continue
		}
		if len(entries) >= pxiRemoteMaxList {
			more = true
			break
		}
		isDir := strings.HasSuffix(line, "/")
		name := strings.TrimSuffix(line, "/")
		p := name
		if dir != "" {
			p = dir + "/" + name
		}
		entries = append(entries, pxiRemoteEntry{Name: name, Path: p, Dir: isDir})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	writeJSON(w, map[string]any{"entries": entries, "more": more})
}

// handlePxiRemoteFile serves /api/file for "@ssh/<id>/<path>", in the same
// shape as a local file, so px0's viewer renders it.
func (s *Server) handlePxiRemoteFile(w http.ResponseWriter, r *http.Request) {
	if !pxiRemoteLocal(w, r) {
		return
	}
	q := r.URL.Query()
	virt := strings.TrimPrefix(q.Get("path"), "/")
	rest := strings.TrimPrefix(virt, pxiRemotePrefix)
	id, inner, _ := strings.Cut(rest, "/")
	rem, ok := pxiRemoteByID(id)
	inner, okRel := pxiRemoteRel(inner)
	if !ok || !okRel || inner == "" {
		fail(w, 400, "bad path")
		return
	}
	start, _ := strconv.Atoi(q.Get("start"))
	key := id + "/" + inner
	pxiRemoteMu.Lock()
	d, size := pxiRemoteDoc.doc, pxiRemoteDoc.size
	if pxiRemoteDoc.key != key || start == 0 { // a fresh open reads the file again
		d = nil
	}
	pxiRemoteMu.Unlock()
	if d == nil {
		ctx, cancel := pxiCtx(r, 60*time.Second)
		defer cancel()
		out, err := pxiSSH(ctx, rem.Host, "head -c "+strconv.Itoa(pxiRemoteMaxBytes+1)+" -- "+pxiShQuote(rem.Path+"/"+inner))
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		if len(out) > pxiRemoteMaxBytes {
			fail(w, 415, "file too large to open over SSH (over 2 MB)")
			return
		}
		if imageExt[strings.ToLower(path.Ext(inner))] || render.IsBinary(out) {
			fail(w, 415, "binary file")
			return
		}
		d = render.NewDoc(strings.ReplaceAll(string(out), "\r\n", "\n"), inner)
		size = len(out)
		pxiRemoteMu.Lock()
		if pxiRemoteDoc.timer != nil {
			pxiRemoteDoc.timer.Stop()
		}
		pxiRemoteDoc.key, pxiRemoteDoc.doc, pxiRemoteDoc.size = key, d, size
		pxiRemoteDoc.timer = time.AfterFunc(time.Minute, func() {
			pxiRemoteMu.Lock()
			if pxiRemoteDoc.key == key {
				pxiRemoteDoc.key, pxiRemoteDoc.doc = "", nil
			}
			pxiRemoteMu.Unlock()
		})
		pxiRemoteMu.Unlock()
	}
	count, _ := strconv.Atoi(q.Get("count"))
	if count <= 0 {
		count = render.ChunkLines
	}
	start = max(0, min(start, d.Total))
	lines, exact := d.Lines(start, start+count)
	_, coming := d.Exact()
	resp := map[string]any{
		"path": virt, "lang": d.Lang, "total": d.Total, "maxCols": d.MaxCols,
		"start": start, "lines": lines, "size": size,
		"exact": exact, "refine": !exact && coming,
		"markdown": render.IsMarkdown(inner), "table": table.IsTable(inner),
		"diffAvailable": false, "remote": rem.Host,
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

// pxiSSHConfigHosts lists the concrete Host names in ~/.ssh/config.
func pxiSSHConfigHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{}
	}
	b, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return []string{}
	}
	hosts := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.EqualFold(f[0], "host") {
			continue
		}
		for _, h := range f[1:] {
			if strings.ContainsAny(h, "*?!") || seen[h] || len(hosts) >= 100 {
				continue
			}
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	return hosts
}

func pxiOwnedByMe(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
