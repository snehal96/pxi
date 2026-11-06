//go:build !windows

package main

// Processes that Claude sessions and pxi terminals started: dev servers,
// watchers, test runs. Listed on request only (the Claude tab asks while it is
// on screen); nothing is tracked in the background. Stop sends SIGTERM, then
// SIGKILL to what is still running 3 seconds later, and only ever to a
// process that descends from a session or terminal pxi started.

import (
	"bufio"
	"bytes"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type pxiProc struct {
	Pid     int     `json:"pid"`
	PPid    int     `json:"ppid"`
	Depth   int     `json:"depth"`
	Cmd     string  `json:"cmd"`
	Label   string  `json:"label,omitempty"` // the command a Claude Bash call ran, without its shell wrapper
	CPU     float64 `json:"cpu"`
	RSSKB   int     `json:"rss"`
	Elapsed string  `json:"elapsed"`
	Ports   []int   `json:"ports,omitempty"`
	Helper  bool    `json:"helper,omitempty"` // Claude Code's own (MCP servers and the like)
}

type pxiProcGroup struct {
	Kind    string    `json:"kind"` // claude | terminal
	ID      string    `json:"id"`   // session id, or terminal id
	Title   string    `json:"title"`
	Where   string    `json:"where"`
	Root    int       `json:"root"`
	Procs   []pxiProc `json:"procs"`
	rootSet bool
}

type pxiPS struct {
	pid, ppid int
	elapsed   string
	cpu       float64
	rss       int
	cmd       string
}

func pxiReadPS() map[int]pxiPS {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,etime=,pcpu=,rss=,args=").Output()
	if err != nil {
		return nil
	}
	m := map[int]pxiPS{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		ppid, _ := strconv.Atoi(f[1])
		cpu, _ := strconv.ParseFloat(f[3], 64)
		rss, _ := strconv.Atoi(f[4])
		m[pid] = pxiPS{pid: pid, ppid: ppid, elapsed: f[2], cpu: cpu, rss: rss, cmd: strings.Join(f[5:], " ")}
	}
	return m
}

// pxiListenPorts: TCP ports the given processes listen on.
func pxiListenPorts(pids []int) map[int][]int {
	ports := map[int][]int{}
	if len(pids) == 0 {
		return ports
	}
	list := make([]string, len(pids))
	for i, p := range pids {
		list[i] = strconv.Itoa(p)
	}
	out, _ := exec.Command("lsof", "-nP", "-a", "-iTCP", "-sTCP:LISTEN", "-Fpn", "-p", strings.Join(list, ",")).Output()
	pid := 0
	seen := map[[2]int]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "p") {
			pid, _ = strconv.Atoi(line[1:])
		} else if strings.HasPrefix(line, "n") && pid > 0 {
			addr := line[1:]
			if i := strings.LastIndex(addr, ":"); i >= 0 {
				if port, err := strconv.Atoi(addr[i+1:]); err == nil && !seen[[2]int{pid, port}] {
					seen[[2]int{pid, port}] = true
					ports[pid] = append(ports[pid], port)
				}
			}
		}
	}
	return ports
}

// Claude Code runs a Bash call as `zsh -c source <snapshot> ... eval '<cmd>' ...`.
var pxiEvalRe = regexp.MustCompile(`eval '((?:[^']|'\\'')*)'`)

func pxiProcLabel(cmd string) string {
	if !strings.Contains(cmd, "shell-snapshots/") {
		return ""
	}
	if m := pxiEvalRe.FindStringSubmatch(cmd); m != nil {
		return "$ " + strings.ReplaceAll(m[1], `'\''`, "'")
	}
	return ""
}

func pxiProcHelper(cmd string) bool {
	c := strings.ToLower(cmd)
	return strings.Contains(c, "mcp") || strings.Contains(c, "--chrome-native-host") || strings.Contains(c, "claude-in-chrome")
}

// pxiProcGroups: every session and terminal with the processes under it.
func (s *Server) pxiProcGroups() []*pxiProcGroup {
	var groups []*pxiProcGroup
	m := pxiClaudeGet()
	m.mu.Lock()
	for _, run := range m.runs {
		g := &pxiProcGroup{Kind: "claude", ID: run.Session, Title: run.Title, Where: run.Where}
		if run.cmd != nil && run.cmd.Process != nil {
			g.Root = run.cmd.Process.Pid
		} else if run.ide != nil {
			g.Root = run.ide.pid
		}
		if g.Root > 0 {
			groups = append(groups, g)
		}
	}
	m.mu.Unlock()
	for _, t := range pxiTermsGet().list() {
		if t.Alive {
			groups = append(groups, &pxiProcGroup{Kind: "terminal", ID: strconv.Itoa(t.ID), Title: t.Label, Where: t.Where, Root: t.Pid})
		}
	}
	if len(groups) == 0 {
		return groups
	}
	ps := pxiReadPS()
	kids := map[int][]int{}
	for _, p := range ps {
		kids[p.ppid] = append(kids[p.ppid], p.pid)
	}
	for _, k := range kids {
		sort.Ints(k)
	}
	roots := map[int]bool{}
	for _, g := range groups {
		roots[g.Root] = true
	}
	var all []int
	for _, g := range groups {
		var walk func(pid, depth int)
		walk = func(pid, depth int) {
			for _, c := range kids[pid] {
				if roots[c] {
					continue // a claude running in this terminal: it has its own group
				}
				p := ps[c]
				g.Procs = append(g.Procs, pxiProc{Pid: c, PPid: pid, Depth: depth, Cmd: p.cmd, CPU: p.cpu, RSSKB: p.rss, Elapsed: p.elapsed, Helper: pxiProcHelper(p.cmd), Label: pxiProcLabel(p.cmd)})
				all = append(all, c)
				if len(g.Procs) < 200 {
					walk(c, depth+1)
				}
			}
		}
		walk(g.Root, 0)
	}
	ports := pxiListenPorts(all)
	for _, g := range groups {
		for i := range g.Procs {
			g.Procs[i].Ports = ports[g.Procs[i].Pid]
		}
	}
	return groups
}

func (s *Server) handlePxiProcs(w http.ResponseWriter, r *http.Request) {
	groups := s.pxiProcGroups()
	out := make([]*pxiProcGroup, 0, len(groups))
	for _, g := range groups {
		if len(g.Procs) > 0 {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	writeJSON(w, map[string]any{"groups": out})
}

// handlePxiProcsStop stops one process (and what it started), or every
// process of a group ("group": id) except Claude Code's own helpers.
func (s *Server) handlePxiProcsStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Pid   int    `json:"pid"`
		Group string `json:"group"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	var targets []int
	for _, g := range s.pxiProcGroups() {
		for _, p := range g.Procs {
			if (b.Pid > 0 && p.Pid == b.Pid) || (b.Group != "" && g.ID == b.Group && p.Depth == 0 && !p.Helper) {
				targets = append(targets, p.Pid)
			}
		}
	}
	if len(targets) == 0 {
		fail(w, http.StatusNotFound, "not a process pxi started")
		return
	}
	for _, pid := range targets {
		pxiStopTree(pid)
	}
	writeJSON(w, map[string]any{"stopped": len(targets)})
}

// pxiStopTree: SIGTERM to the process and its descendants (a dev server's
// workers), SIGKILL to whatever is left after 3 seconds.
func pxiStopTree(pid int) {
	ps := pxiReadPS()
	kids := map[int][]int{}
	for _, p := range ps {
		kids[p.ppid] = append(kids[p.ppid], p.pid)
	}
	var tree []int
	var walk func(int)
	walk = func(p int) {
		tree = append(tree, p)
		for _, c := range kids[p] {
			if len(tree) < 500 {
				walk(c)
			}
		}
	}
	walk(pid)
	for _, p := range tree {
		syscall.Kill(p, syscall.SIGTERM)
	}
	go func() {
		time.Sleep(3 * time.Second)
		for _, p := range tree {
			if syscall.Kill(p, 0) == nil {
				syscall.Kill(p, syscall.SIGKILL)
			}
		}
	}()
}
