//go:build !windows

package main

// pxi terminal: shells in pseudo-terminals, driven by web/src/pxi-term.js.
//
// One event stream (/api/pxi/term/events) carries the output of every
// terminal, so the page holds one extra connection only while the terminal
// panel is open. Each terminal keeps its last pxiTermKeep bytes of output so a
// panel that was closed (and freed its screens) can redraw on reopen; nothing
// else is kept. Terminals run until closed or until pxi exits (closing the
// pseudo-terminal hangs up the shell and its jobs).
//
// A terminal runs any command as the user, so it answers only requests from
// this machine: loopback client address and loopback Host, on top of the
// same-origin checks every POST gets (localPost, hostGuard).

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	pxiTermKeep = 256 << 10 // output kept per terminal for redraws
	pxiTermMax  = 12        // terminals open at once
)

type pxiTerm struct {
	ID    int    `json:"id"`
	Label string `json:"label"` // shell name, plus the worktree it runs in
	Cwd   string `json:"cwd"`
	Where string `json:"where"` // "" for the workspace, else the worktree name
	Pid   int    `json:"pid"`
	Alive bool   `json:"alive"`
	Exit  int    `json:"exit"`

	ptmx *os.File
	cmd  *exec.Cmd
	buf  []byte // last pxiTermKeep bytes of output
}

type pxiTermEvent struct {
	ID   int    `json:"t"`
	Data string `json:"d,omitempty"` // base64: output may split UTF-8 sequences
	Exit *int   `json:"exit,omitempty"`
}

type pxiTerms struct {
	mu   sync.Mutex
	next int
	m    map[int]*pxiTerm
	subs map[chan pxiTermEvent]struct{}
}

var pxiTermsOnce sync.Once
var pxiTermsAll *pxiTerms

func pxiTermsGet() *pxiTerms {
	pxiTermsOnce.Do(func() {
		pxiTermsAll = &pxiTerms{m: map[int]*pxiTerm{}, subs: map[chan pxiTermEvent]struct{}{}}
	})
	return pxiTermsAll
}

func (s *Server) registerPxiTermRoutes() {
	s.mux.HandleFunc(s.routePath("/api/pxi/term/list"), s.pxiTermLocal(s.handlePxiTermList))
	s.mux.HandleFunc(s.routePath("/api/pxi/term/events"), s.pxiTermLocal(s.handlePxiTermEvents))
	s.mux.HandleFunc(s.routePath("/api/pxi/term/open"), s.pxiTermLocal(s.handlePxiTermOpen))
	s.mux.HandleFunc(s.routePath("/api/pxi/term/input"), s.pxiTermLocal(s.handlePxiTermInput))
	s.mux.HandleFunc(s.routePath("/api/pxi/term/resize"), s.pxiTermLocal(s.handlePxiTermResize))
	s.mux.HandleFunc(s.routePath("/api/pxi/term/close"), s.pxiTermLocal(s.handlePxiTermClose))
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// pxiTermLocal admits only requests from this machine.
func (s *Server) pxiTermLocal(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.RemoteAddr) || !isLoopbackHost(r.Host) {
			fail(w, http.StatusForbidden, "the terminal is only available on this machine")
			return
		}
		if r.Method == http.MethodPost && !localPost(w, r) {
			return
		}
		h(w, r)
	}
}

func (t *pxiTerms) broadcast(ev pxiTermEvent) {
	for ch := range t.subs {
		select {
		case ch <- ev:
		default: // a stalled page: drop it; it reconnects and redraws from buf
			delete(t.subs, ch)
			close(ch)
		}
	}
}

func (t *pxiTerms) list() []pxiTerm {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]pxiTerm, 0, len(t.m))
	for id := 1; id <= t.next; id++ {
		if term := t.m[id]; term != nil {
			out = append(out, *term)
		}
	}
	return out
}

func (s *Server) handlePxiTermList(w http.ResponseWriter, r *http.Request) {
	list := pxiTermsGet().list()
	for i := range list {
		list[i].buf = nil
	}
	writeJSON(w, map[string]any{"terminals": list})
}

// handlePxiTermEvents streams output. It first replays what each terminal kept,
// then live output, as "data: {t, d}" lines.
func (s *Server) handlePxiTermEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	t := pxiTermsGet()
	ch := make(chan pxiTermEvent, 512)
	t.mu.Lock()
	var replay []pxiTermEvent
	for id := 1; id <= t.next; id++ {
		if term := t.m[id]; term != nil {
			replay = append(replay, pxiTermEvent{ID: id, Data: base64.StdEncoding.EncodeToString(term.buf)})
			if !term.Alive {
				code := term.Exit
				replay = append(replay, pxiTermEvent{ID: id, Exit: &code})
			}
		}
	}
	t.subs[ch] = struct{}{}
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		if _, ok := t.subs[ch]; ok {
			delete(t.subs, ch)
			close(ch)
		}
		t.mu.Unlock()
	}()

	send := func(ev pxiTermEvent) bool {
		b, _ := json.Marshal(ev)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		return true
	}
	fmt.Fprint(w, ": pxi terminal\n\n")
	for _, ev := range replay {
		if !send(ev) {
			return
		}
	}
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok || !send(ev) {
				return
			}
			// Drain what is already queued before flushing, so a burst of
			// output is one write instead of hundreds.
			for n := len(ch); n > 0; n-- {
				if ev, ok = <-ch; !ok || !send(ev) {
					return
				}
			}
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// handlePxiTermOpen: {"cwd": "" | "@wt/<name>", "cols", "rows"} starts a login
// shell in the workspace or in one of its worktrees.
func (s *Server) handlePxiTermOpen(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Cwd  string `json:"cwd"`
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	dir, where := s.ix.Root(), ""
	if isWTPath(b.Cwd) {
		abs, _, wt, _, ok := s.worktrees().resolve(b.Cwd)
		if !ok {
			fail(w, http.StatusBadRequest, "unknown worktree")
			return
		}
		dir, where = abs, wt.Name
	} else if b.Cwd != "" {
		fail(w, http.StatusBadRequest, "bad cwd")
		return
	}
	t := pxiTermsGet()
	t.mu.Lock()
	open := 0
	for _, term := range t.m {
		if term.Alive {
			open++
		}
	}
	t.mu.Unlock()
	if open >= pxiTermMax {
		fail(w, http.StatusTooManyRequests, fmt.Sprintf("%d terminals are open; close one first", pxiTermMax))
		return
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=pxi")
	// `claude` started here connects to pxi as its IDE (fork_ide.go).
	if ide, err := s.pxiIDEStart(); err == nil {
		cmd.Env = append(cmd.Env, ide.TermEnv()...)
	}
	size := &pty.Winsize{Cols: uint16(clampInt(b.Cols, 20, 500, 100)), Rows: uint16(clampInt(b.Rows, 5, 300, 24))}
	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not start a shell: "+err.Error())
		return
	}
	label := filepath.Base(shell)
	if where != "" {
		label += " · " + where
	}
	t.mu.Lock()
	t.next++
	term := &pxiTerm{ID: t.next, Label: label, Cwd: dir, Where: where, Pid: cmd.Process.Pid, Alive: true, ptmx: ptmx, cmd: cmd}
	t.m[term.ID] = term
	t.mu.Unlock()

	go t.pump(term)
	writeJSON(w, term)
}

func clampInt(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	return max(lo, min(hi, v))
}

// pump copies a terminal's output to its buffer and the subscribers, then
// records the exit.
func (t *pxiTerms) pump(term *pxiTerm) {
	chunk := make([]byte, 32<<10)
	for {
		n, err := term.ptmx.Read(chunk)
		if n > 0 {
			data := chunk[:n]
			t.mu.Lock()
			term.buf = append(term.buf, data...)
			if over := len(term.buf) - pxiTermKeep; over > 0 {
				term.buf = append(term.buf[:0], term.buf[over:]...)
			}
			t.broadcast(pxiTermEvent{ID: term.ID, Data: base64.StdEncoding.EncodeToString(data)})
			t.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	code := 0
	if err := term.cmd.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	term.ptmx.Close()
	t.mu.Lock()
	term.Alive, term.Exit = false, code
	t.broadcast(pxiTermEvent{ID: term.ID, Exit: &code})
	t.mu.Unlock()
}

func (t *pxiTerms) get(id int) *pxiTerm {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.m[id]
}

// handlePxiTermInput: {"id", "data"} writes keystrokes to the shell.
func (s *Server) handlePxiTermInput(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID   int    `json:"id"`
		Data string `json:"data"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	term := pxiTermsGet().get(b.ID)
	if term == nil || !term.Alive {
		fail(w, http.StatusGone, "terminal closed")
		return
	}
	if _, err := term.ptmx.Write([]byte(b.Data)); err != nil {
		fail(w, http.StatusGone, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handlePxiTermResize: {"id", "cols", "rows"}.
func (s *Server) handlePxiTermResize(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID   int `json:"id"`
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	term := pxiTermsGet().get(b.ID)
	if term == nil || !term.Alive {
		fail(w, http.StatusGone, "terminal closed")
		return
	}
	_ = pty.Setsize(term.ptmx, &pty.Winsize{Cols: uint16(clampInt(b.Cols, 20, 500, 100)), Rows: uint16(clampInt(b.Rows, 5, 300, 24))})
	writeJSON(w, map[string]any{"ok": true})
}

// handlePxiTermClose: {"id"} hangs up the shell (and the jobs it started) and
// forgets the terminal.
func (s *Server) handlePxiTermClose(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID int `json:"id"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	t := pxiTermsGet()
	t.mu.Lock()
	term := t.m[b.ID]
	delete(t.m, b.ID)
	t.mu.Unlock()
	if term == nil {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if term.Alive && term.cmd.Process != nil {
		// The shell leads its own session: hang up the whole process group,
		// then make sure after a grace period.
		pid := term.cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGHUP)
		term.ptmx.Close()
		go func() {
			time.Sleep(2 * time.Second)
			t.mu.Lock()
			alive := term.Alive // pump clears it once the shell has exited
			t.mu.Unlock()
			if alive {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}()
	}
	writeJSON(w, map[string]any{"ok": true})
}
