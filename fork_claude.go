//go:build !windows

package main

// pxi Claude panel: Claude Code sessions run by the user's own `claude` CLI,
// so they use the user's own login (subscription or key), driven by
// web/pxi/claude.js.
//
// Each open session is one `claude -p` process speaking stream-json on stdin
// and stdout. Tool permissions come back to pxi as control requests
// (--permission-prompt-tool stdio), so edits are shown as diffs and approved
// here. A process exits after pxiClaudeIdle without a message (Rule zero: no
// idle agents); the next message resumes the session from its transcript.
//
// Accounts are Claude Code config folders (~/.claude, ~/.claude-<name>); the
// chosen one is remembered per repository. Sessions are read from the
// account's transcripts, so sessions started in a terminal show up too.
//
// Starting processes is as powerful as the terminal, so the same rule applies:
// only requests from this machine (pxiTermLocal).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"px0/internal/settings"
)

const (
	pxiClaudeIdle       = 10 * time.Minute // a process with nothing to do exits after this
	pxiClaudeMaxRuns    = 8                // sessions running at once
	pxiClaudeKeep       = 2000             // events a running session keeps for a reopened panel
	pxiClaudeSessions   = 25               // sessions listed per place
	pxiClaudeHistoryMax = 400              // events loaded from a transcript
	pxiClaudeBodyMax    = 1 << 20          // a message, with any pasted text
)

// ---------------------------------------------------------------- accounts

type pxiClaudeAccount struct {
	ID    string `json:"id"`    // the folder name: ".claude", ".claude-work"
	Label string `json:"label"` // "default", "work"
	Email string `json:"email,omitempty"`
	Org   string `json:"org,omitempty"`
	dir   string
}

// pxiClaudeAccounts lists Claude Code config folders in the home folder that
// have been logged in (they hold a .claude.json; the default folder keeps it
// next to itself).
func pxiClaudeAccounts() []pxiClaudeAccount {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	read := func(p string) (email, org string, ok bool) {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", "", false
		}
		var j struct {
			OAuth *struct {
				Email string `json:"emailAddress"`
				Org   string `json:"organizationName"`
			} `json:"oauthAccount"`
		}
		if json.Unmarshal(b, &j) != nil {
			return "", "", false
		}
		if j.OAuth != nil {
			return j.OAuth.Email, j.OAuth.Org, true
		}
		return "", "", true
	}
	var out []pxiClaudeAccount
	if email, org, ok := read(filepath.Join(home, ".claude.json")); ok {
		out = append(out, pxiClaudeAccount{ID: ".claude", Label: "default", Email: email, Org: org, dir: filepath.Join(home, ".claude")})
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, ".claude-") {
			continue
		}
		dir := filepath.Join(home, name)
		if email, org, ok := read(filepath.Join(dir, ".claude.json")); ok {
			out = append(out, pxiClaudeAccount{ID: name, Label: strings.TrimPrefix(name, ".claude-"), Email: email, Org: org, dir: dir})
		}
	}
	return out
}

func pxiClaudeAccountByID(id string) (pxiClaudeAccount, bool) {
	for _, a := range pxiClaudeAccounts() {
		if a.ID == id {
			return a, true
		}
	}
	return pxiClaudeAccount{}, false
}

// The account chosen per repository, in a file next to settings.json.
func pxiClaudeChoicePath() string {
	return filepath.Join(filepath.Dir(settings.Path()), "claude-accounts.json")
}

var pxiClaudeChoiceMu sync.Mutex

func pxiClaudeChosen(root string) string {
	pxiClaudeChoiceMu.Lock()
	defer pxiClaudeChoiceMu.Unlock()
	m := map[string]string{}
	if b, err := os.ReadFile(pxiClaudeChoicePath()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m[root]
}

func pxiClaudeChoose(root, id string) error {
	pxiClaudeChoiceMu.Lock()
	defer pxiClaudeChoiceMu.Unlock()
	m := map[string]string{}
	if b, err := os.ReadFile(pxiClaudeChoicePath()); err == nil {
		json.Unmarshal(b, &m)
	}
	m[root] = id
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(filepath.Dir(pxiClaudeChoicePath()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(pxiClaudeChoicePath(), b, 0o600)
}

// pxiClaudeAccountFor returns the repo's chosen account, else the only or
// first one.
func pxiClaudeAccountFor(root, asked string) (pxiClaudeAccount, bool) {
	if asked != "" {
		return pxiClaudeAccountByID(asked)
	}
	all := pxiClaudeAccounts()
	if len(all) == 0 {
		return pxiClaudeAccount{}, false
	}
	if id := pxiClaudeChosen(root); id != "" {
		for _, a := range all {
			if a.ID == id {
				return a, true
			}
		}
	}
	return all[0], true
}

// ---------------------------------------------------------------- transcripts

var pxiClaudeNonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// Claude Code keeps a folder's sessions in <config>/projects/<folder path with
// every non-alphanumeric character turned into "-">.
func pxiClaudeProjectDir(acc pxiClaudeAccount, cwd string) string {
	return filepath.Join(acc.dir, "projects", pxiClaudeNonAlnum.ReplaceAllString(evalPath(cwd), "-"))
}

var pxiClaudeIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var pxiClaudeAnyIDRe = regexp.MustCompile(`^([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|ide-[0-9]{1,6})$`)

type pxiClaudeSession struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated int64  `json:"updated"` // unix ms
	Live    bool   `json:"live,omitempty"`
	Busy    bool   `json:"busy,omitempty"`
	Asking  int    `json:"asking,omitempty"`
}

// pxiClaudeTitle: the latest AI title near the end of the transcript, else the
// first thing the user typed.
func pxiClaudeTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, _ := f.Stat()
	const tail = 256 << 10
	if st != nil && st.Size() > 0 {
		off := max(0, st.Size()-tail)
		buf := make([]byte, st.Size()-off)
		if _, err := f.ReadAt(buf, off); err == nil || err == io.EOF {
			title := ""
			for _, line := range bytes.Split(buf, []byte("\n")) {
				if !bytes.Contains(line, []byte(`"ai-title"`)) {
					continue
				}
				var e struct {
					Type  string `json:"type"`
					Title string `json:"aiTitle"`
				}
				if json.Unmarshal(line, &e) == nil && e.Type == "ai-title" && e.Title != "" {
					title = e.Title
				}
			}
			if title != "" {
				return title
			}
		}
	}
	f.Seek(0, 0)
	r := bufio.NewReaderSize(io.LimitReader(f, 2<<20), 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && bytes.Contains(line, []byte(`"type":"user"`)) {
			var e pxiClaudeEntry
			if json.Unmarshal(line, &e) == nil && e.Type == "user" && !e.IsMeta && !e.IsSidechain {
				if t := pxiClaudeUserText(e.Message.Content); t != "" && !strings.HasPrefix(t, "<") {
					return pxiClaudeShort(t, 90)
				}
			}
		}
		if err != nil {
			return ""
		}
	}
}

func pxiClaudeShort(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func pxiClaudeListSessions(acc pxiClaudeAccount, cwd string) []pxiClaudeSession {
	dir := pxiClaudeProjectDir(acc, cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []pxiClaudeSession{}
	}
	type f struct {
		id  string
		mod time.Time
	}
	var files []f
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || !pxiClaudeIDRe.MatchString(id) {
			continue
		}
		if info, err := e.Info(); err == nil && info.Size() > 0 {
			files = append(files, f{id, info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > pxiClaudeSessions {
		files = files[:pxiClaudeSessions]
	}
	out := make([]pxiClaudeSession, 0, len(files))
	for _, x := range files {
		title := pxiClaudeTitle(filepath.Join(dir, x.id+".jsonl"))
		if title == "" {
			continue // nothing typed: a session that never started
		}
		out = append(out, pxiClaudeSession{ID: x.id, Title: title, Updated: x.mod.UnixMilli()})
	}
	return out
}

// One line of a transcript (only what the panel shows).
type pxiClaudeEntry struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type pxiClaudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func pxiClaudeBlocks(content json.RawMessage) (string, []pxiClaudeBlock) {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s, nil
	}
	var blocks []pxiClaudeBlock
	json.Unmarshal(content, &blocks)
	return "", blocks
}

func pxiClaudeUserText(content json.RawMessage) string {
	s, blocks := pxiClaudeBlocks(content)
	if s != "" {
		return s
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// pxiClaudeEv is one event the panel draws. k: user, text, delta, tool,
// result, ask, answered, init, done, limit, notice, exit, idle, busy.
type pxiClaudeEv map[string]any

// pxiClaudeMessageEvents turns a user or assistant message into events.
func pxiClaudeMessageEvents(role string, content, toolResult json.RawMessage) []pxiClaudeEv {
	var out []pxiClaudeEv
	s, blocks := pxiClaudeBlocks(content)
	if role == "user" {
		if s != "" {
			if t := pxiClaudeCleanUser(s); t != "" {
				out = append(out, pxiClaudeEv{"k": "user", "text": t})
			}
			return out
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if t := pxiClaudeCleanUser(b.Text); t != "" {
					out = append(out, pxiClaudeEv{"k": "user", "text": t})
				}
			case "tool_result":
				ev := pxiClaudeEv{"k": "result", "id": b.ToolUseID, "error": b.IsError, "text": pxiClaudeShort(pxiClaudeUserText(b.Content), 600)}
				if patch := pxiClaudePatch(toolResult); patch != "" {
					ev["diff"] = patch
				}
				out = append(out, ev)
			}
		}
		return out
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				out = append(out, pxiClaudeEv{"k": "text", "text": b.Text})
			}
		case "tool_use":
			out = append(out, pxiClaudeEv{"k": "tool", "id": b.ID, "name": b.Name, "label": pxiClaudeToolLabel(b.Name, b.Input), "input": pxiClaudeSmallInput(b.Name, b.Input)})
		}
	}
	return out
}

// What the user typed, without the context blocks Claude Code adds around it.
var pxiClaudeNoise = regexp.MustCompile(`(?s)<(system-reminder|ide_selection|ide_opened_file|local-command-stdout|local-command-caveat|command-message|command-args)>.*?</(system-reminder|ide_selection|ide_opened_file|local-command-stdout|local-command-caveat|command-message|command-args)>`)

func pxiClaudeCleanUser(s string) string {
	s = pxiClaudeNoise.ReplaceAllString(s, "")
	if m := regexp.MustCompile(`<command-name>(.*?)</command-name>`).FindStringSubmatch(s); m != nil {
		return m[1]
	}
	if strings.HasPrefix(strings.TrimSpace(s), "<task-notification>") {
		return ""
	}
	return strings.TrimSpace(s)
}

func pxiClaudeToolLabel(name string, input json.RawMessage) string {
	var m map[string]any
	json.Unmarshal(input, &m)
	str := func(k string) string { v, _ := m[k].(string); return v }
	switch name {
	case "Bash":
		if d := str("description"); d != "" {
			return d
		}
		return pxiClaudeShort(str("command"), 120)
	case "Read", "Edit", "MultiEdit", "Write", "NotebookEdit":
		return str("file_path") + str("notebook_path")
	case "Grep", "Glob":
		return str("pattern")
	case "WebFetch":
		return str("url")
	case "WebSearch":
		return str("query")
	case "Task", "Agent":
		return str("description")
	case "TodoWrite":
		return "update to-dos"
	}
	return ""
}

// The tool input the panel needs: small fields only; file contents become diffs.
func pxiClaudeSmallInput(name string, input json.RawMessage) map[string]any {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return nil
	}
	for k, v := range m {
		if s, ok := v.(string); ok && len(s) > 4000 {
			m[k] = s[:4000] + "…"
		}
	}
	switch name {
	case "Edit", "MultiEdit", "Write":
		delete(m, "old_string")
		delete(m, "new_string")
		delete(m, "edits")
		delete(m, "content")
	}
	return m
}

// pxiClaudePatch renders a tool result's structuredPatch (Edit, Write) as a
// unified diff.
func pxiClaudePatch(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] != '{' {
		return ""
	}
	var r struct {
		FilePath string `json:"filePath"`
		Patch    []struct {
			OldStart int      `json:"oldStart"`
			OldLines int      `json:"oldLines"`
			NewStart int      `json:"newStart"`
			NewLines int      `json:"newLines"`
			Lines    []string `json:"lines"`
		} `json:"structuredPatch"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.Patch) == 0 {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, h := range r.Patch {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, l := range h.Lines {
			if n++; n > 400 {
				b.WriteString(" …\n")
				return b.String()
			}
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// pxiClaudeHistory reads a transcript's last events, up to byte `end` (-1: all).
func pxiClaudeHistory(path string, end int64) []pxiClaudeEv {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	size := st.Size()
	if end >= 0 && end < size {
		size = end
	}
	const window = 8 << 20
	off := max(0, size-window)
	buf := make([]byte, size-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil
	}
	lines := bytes.Split(buf, []byte("\n"))
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // cut mid-line
	}
	var out []pxiClaudeEv
	for _, line := range lines {
		if !bytes.Contains(line, []byte(`"type":"user"`)) && !bytes.Contains(line, []byte(`"type":"assistant"`)) {
			continue
		}
		var e pxiClaudeEntry
		if json.Unmarshal(line, &e) != nil || e.IsMeta || e.IsSidechain || (e.Type != "user" && e.Type != "assistant") {
			continue
		}
		out = append(out, pxiClaudeMessageEvents(e.Type, e.Message.Content, e.ToolUseResult)...)
	}
	if len(out) > pxiClaudeHistoryMax {
		out = append([]pxiClaudeEv{{"k": "notice", "text": "Earlier messages are not shown."}}, out[len(out)-pxiClaudeHistoryMax:]...)
	}
	return out
}

// ---------------------------------------------------------------- running sessions

type pxiClaudeRun struct {
	Session string `json:"session"`
	Account string `json:"account"`
	Where   string `json:"where"` // "" for the workspace, else the worktree name
	Title   string `json:"title"`
	Busy    bool   `json:"busy"`
	Asking  int    `json:"asking"`
	Mode    string `json:"mode"`
	Model   string `json:"model"`

	cwd     string
	histEnd int64 // transcript size when the process started: history before, events after
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	events  []pxiClaudeEv
	asks    map[string]pxiClaudeEv // pending permission requests by request id
	idle    *time.Timer
	stderr  bytes.Buffer
	reqSeq  int
	ide     *pxiIDEClient // a terminal claude connected through the IDE bridge
}

type pxiClaudeMgr struct {
	mu   sync.Mutex
	runs map[string]*pxiClaudeRun
	subs map[string]map[chan pxiClaudeEv]struct{} // by session
	all  map[chan struct{}]struct{}               // pages watching the session list (badges)

	pageReqs []map[string]any // requests from IDE clients for the page (open a file)
}

var pxiClaudeOnce sync.Once
var pxiClaudeAll *pxiClaudeMgr

func pxiClaudeGet() *pxiClaudeMgr {
	pxiClaudeOnce.Do(func() {
		pxiClaudeAll = &pxiClaudeMgr{runs: map[string]*pxiClaudeRun{}, subs: map[string]map[chan pxiClaudeEv]struct{}{}, all: map[chan struct{}]struct{}{}}
	})
	return pxiClaudeAll
}

// emitLocked records an event for replay (deltas are live only) and sends it.
func (m *pxiClaudeMgr) emitLocked(run *pxiClaudeRun, ev pxiClaudeEv) {
	if ev["k"] != "delta" {
		run.events = append(run.events, ev)
		if over := len(run.events) - pxiClaudeKeep; over > 0 {
			run.events = append([]pxiClaudeEv{{"k": "notice", "text": "Earlier messages of this run are not shown."}}, run.events[over+1:]...)
		}
	}
	for ch := range m.subs[run.Session] {
		select {
		case ch <- ev:
		default: // a stalled page: drop it; it reconnects and replays
			delete(m.subs[run.Session], ch)
			close(ch)
		}
	}
}

// changedLocked tells pages showing the session list that a status changed.
func (m *pxiClaudeMgr) changedLocked() {
	for ch := range m.all {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func pxiClaudeBin() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local/bin/claude"), filepath.Join(home, ".claude/local/claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

func pxiClaudeEnv(acc pxiClaudeAccount) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "CLAUDE_CONFIG_DIR", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT":
			continue
		}
		env = append(env, kv)
	}
	if acc.ID != ".claude" {
		env = append(env, "CLAUDE_CONFIG_DIR="+acc.dir)
	}
	return env
}

var pxiClaudeModes = map[string]bool{"default": true, "acceptEdits": true, "plan": true, "auto": true}
var pxiClaudeModelRe = regexp.MustCompile(`^[a-zA-Z0-9._\-\[\]]{0,64}$`)

// start launches the process for a session (new or resumed). Caller holds m.mu.
func (m *pxiClaudeMgr) startLocked(acc pxiClaudeAccount, cwd, where, session string, resume bool, model, mode string) (*pxiClaudeRun, error) {
	busy := 0
	for _, r := range m.runs {
		if r.cmd != nil {
			busy++
		}
	}
	if busy >= pxiClaudeMaxRuns {
		return nil, fmt.Errorf("%d Claude sessions are running; stop one first", pxiClaudeMaxRuns)
	}
	bin := pxiClaudeBin()
	if bin == "" {
		return nil, fmt.Errorf("Claude Code is not installed (no `claude` on PATH). Install it from https://claude.com/claude-code")
	}
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool", "stdio"}
	if resume {
		args = append(args, "--resume", session)
	} else {
		args = append(args, "--session-id", session)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if mode != "" && mode != "default" {
		args = append(args, "--permission-mode", mode)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = cwd
	cmd.Env = pxiClaudeEnv(acc)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // Stop ends what the session started too
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	run := &pxiClaudeRun{Session: session, Account: acc.ID, Where: where, Mode: mode, Model: model, cwd: cwd, cmd: cmd, stdin: stdin, asks: map[string]pxiClaudeEv{}, histEnd: -1}
	if fi, err := os.Stat(filepath.Join(pxiClaudeProjectDir(acc, cwd), session+".jsonl")); err == nil {
		run.histEnd = fi.Size()
	} else {
		run.histEnd = 0
	}
	cmd.Stderr = &pxiCapped{b: &run.stderr, max: 8 << 10}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start claude: %v", err)
	}
	if old := m.runs[session]; old != nil {
		run.Title = old.Title
	}
	m.runs[session] = run
	go m.read(run, stdout)
	m.changedLocked()
	return run, nil
}

type pxiCapped struct {
	mu  sync.Mutex
	b   *bytes.Buffer
	max int
}

func (c *pxiCapped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.b.Write(p)
	if over := c.b.Len() - c.max; over > 0 {
		c.b.Next(over)
	}
	return len(p), nil
}

// writeLocked sends one stream-json line to the process.
func (run *pxiClaudeRun) writeLocked(v any) error {
	if run.stdin == nil {
		return fmt.Errorf("session is not running")
	}
	b, _ := json.Marshal(v)
	_, err := run.stdin.Write(append(b, '\n'))
	return err
}

func (run *pxiClaudeRun) controlLocked(req map[string]any) error {
	run.reqSeq++
	return run.writeLocked(map[string]any{"type": "control_request", "request_id": fmt.Sprintf("pxi-%d", run.reqSeq), "request": req})
}

// armIdle (re)starts the idle timer: a session with no turn running and no
// question pending ends its process after pxiClaudeIdle.
func (m *pxiClaudeMgr) armIdleLocked(run *pxiClaudeRun) {
	if run.idle != nil {
		run.idle.Stop()
	}
	run.idle = time.AfterFunc(pxiClaudeIdle, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if !run.Busy && len(run.asks) == 0 && run.stdin != nil {
			run.stdin.Close() // claude exits when its input ends
			run.stdin = nil
		}
	})
}

func (m *pxiClaudeMgr) read(run *pxiClaudeRun, stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 256<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			m.handleLine(run, line)
		}
		if err != nil {
			break
		}
	}
	waitErr := run.cmd.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	code := run.cmd.ProcessState.ExitCode()
	ev := pxiClaudeEv{"k": "exit", "code": code}
	if waitErr != nil && code != 0 {
		ev["error"] = strings.TrimSpace(run.stderr.String())
	}
	if run.idle != nil {
		run.idle.Stop()
	}
	for id := range run.asks {
		m.emitLocked(run, pxiClaudeEv{"k": "answered", "req": id, "allow": false})
	}
	run.asks = map[string]pxiClaudeEv{}
	run.Busy = false
	run.cmd, run.stdin = nil, nil
	m.emitLocked(run, ev)
	// The transcript has everything now; forget the run.
	if m.runs[run.Session] == run {
		delete(m.runs, run.Session)
	}
	m.changedLocked()
}

type pxiClaudeLine struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	Message   json.RawMessage `json:"message"`
	Event     json.RawMessage `json:"event"`
	ParentID  *string         `json:"parent_tool_use_id"`
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
	ToolUse   json.RawMessage `json:"tool_use_result"`
	// init
	Model          string `json:"model"`
	PermissionMode string `json:"permissionMode"`
	// result
	IsError    bool   `json:"is_error"`
	Result     string `json:"result"`
	DurationMs int    `json:"duration_ms"`
	NumTurns   int    `json:"num_turns"`
	// rate limits
	RateLimit json.RawMessage `json:"rate_limit_info"`
	// notices
	Text string `json:"message_text"`
}

func (m *pxiClaudeMgr) handleLine(run *pxiClaudeRun, line []byte) {
	var l pxiClaudeLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sub := l.ParentID != nil && *l.ParentID != "" // a subagent's own messages: not shown
	switch l.Type {
	case "system":
		switch l.Subtype {
		case "init":
			if l.Model != "" {
				run.Model = l.Model
			}
			if l.PermissionMode != "" {
				run.Mode = l.PermissionMode
			}
			m.emitLocked(run, pxiClaudeEv{"k": "init", "model": run.Model, "mode": run.Mode})
		case "compact_boundary":
			m.emitLocked(run, pxiClaudeEv{"k": "notice", "text": "Conversation compacted."})
		}
	case "stream_event":
		if sub {
			return
		}
		var e struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal(l.Event, &e) == nil && e.Type == "content_block_delta" && e.Delta.Type == "text_delta" {
			m.emitLocked(run, pxiClaudeEv{"k": "delta", "text": e.Delta.Text})
		}
	case "assistant", "user":
		if sub {
			return
		}
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		json.Unmarshal(l.Message, &msg)
		for _, ev := range pxiClaudeMessageEvents(l.Type, msg.Content, l.ToolUse) {
			if ev["k"] == "user" {
				continue // the panel shows what it sent itself
			}
			m.emitLocked(run, ev)
		}
	case "result":
		run.Busy = false
		ev := pxiClaudeEv{"k": "done", "ok": !l.IsError && l.Subtype == "success", "ms": l.DurationMs, "turns": l.NumTurns}
		if l.IsError || l.Subtype != "success" {
			ev["error"] = pxiClaudeShort(l.Result, 600)
		}
		m.emitLocked(run, ev)
		m.armIdleLocked(run)
		m.changedLocked()
	case "rate_limit_event":
		var info struct {
			Status   string `json:"status"`
			ResetsAt int64  `json:"resetsAt"`
			Type     string `json:"rateLimitType"`
		}
		if json.Unmarshal(l.RateLimit, &info) == nil && info.Status != "" && info.Status != "allowed" {
			m.emitLocked(run, pxiClaudeEv{"k": "limit", "status": info.Status, "resets": info.ResetsAt, "kind": info.Type})
		}
	case "control_request":
		m.controlRequestLocked(run, l.RequestID, l.Request)
	case "control_response":
		// The answer to pxi's own request (interrupt, mode, model).
		var c struct {
			Response struct {
				Subtype string `json:"subtype"`
				Error   string `json:"error"`
			} `json:"response"`
		}
		if json.Unmarshal(line, &c) == nil && c.Response.Subtype == "error" {
			m.emitLocked(run, pxiClaudeEv{"k": "notice", "text": "Claude: " + pxiClaudeShort(c.Response.Error, 200)})
		}
	}
}

// controlRequestLocked: Claude asks pxi whether a tool may run.
func (m *pxiClaudeMgr) controlRequestLocked(run *pxiClaudeRun, id string, raw json.RawMessage) {
	var req struct {
		Subtype     string          `json:"subtype"`
		ToolName    string          `json:"tool_name"`
		Input       json.RawMessage `json:"input"`
		Description string          `json:"description"`
		ToolUseID   string          `json:"tool_use_id"`
	}
	json.Unmarshal(raw, &req)
	if req.Subtype != "can_use_tool" {
		run.writeLocked(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "error", "request_id": id, "error": "not supported by pxi"}})
		return
	}
	var input map[string]any
	json.Unmarshal(req.Input, &input)
	ev := pxiClaudeEv{"k": "ask", "req": id, "tool": req.ToolName, "label": pxiClaudeToolLabel(req.ToolName, req.Input), "input": pxiClaudeSmallInput(req.ToolName, req.Input), "tid": req.ToolUseID}
	switch req.ToolName {
	case "Edit", "MultiEdit", "Write":
		ev["diff"] = pxiClaudeProposedDiff(req.ToolName, input)
	case "ExitPlanMode", "AskUserQuestion":
		ev["input"] = input // the plan text and questions are the point
	}
	run.asks[id] = pxiClaudeEv{"ev": ev, "input": input}
	run.Asking = len(run.asks)
	if run.idle != nil {
		run.idle.Stop()
	}
	m.emitLocked(run, ev)
	m.changedLocked()
}

// pxiClaudeProposedDiff shows what an edit would change, as a unified diff
// against the file on disk.
func pxiClaudeProposedDiff(tool string, in map[string]any) string {
	path, _ := in["file_path"].(string)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	old := ""
	if fi, err := os.Stat(path); err == nil {
		if fi.IsDir() || fi.Size() > 4<<20 {
			return ""
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		old = string(b)
	}
	next := old
	apply := func(o, n string, all bool) {
		if o == "" {
			return
		}
		if all {
			next = strings.ReplaceAll(next, o, n)
		} else {
			next = strings.Replace(next, o, n, 1)
		}
	}
	switch tool {
	case "Write":
		next, _ = in["content"].(string)
	case "Edit":
		o, _ := in["old_string"].(string)
		n, _ := in["new_string"].(string)
		all, _ := in["replace_all"].(bool)
		apply(o, n, all)
	case "MultiEdit":
		edits, _ := in["edits"].([]any)
		for _, e := range edits {
			em, _ := e.(map[string]any)
			o, _ := em["old_string"].(string)
			n, _ := em["new_string"].(string)
			all, _ := em["replace_all"].(bool)
			apply(o, n, all)
		}
	}
	return pxiUnifiedDiff(old, next)
}

// pxiUnifiedDiff: git's diff of two texts, hunks only (no headers).
func pxiUnifiedDiff(a, b string) string {
	dir, err := os.MkdirTemp("", "pxi-diff-")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(dir)
	pa, pb := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(pa, []byte(a), 0o600)
	os.WriteFile(pb, []byte(b), 0o600)
	out, _ := exec.Command("git", "diff", "--no-index", "--no-color", "--unified=3", pa, pb).Output()
	s := string(out)
	if i := strings.Index(s, "\n@@"); i >= 0 {
		s = s[i+1:]
	} else {
		return ""
	}
	if lines := strings.SplitAfter(s, "\n"); len(lines) > 600 {
		s = strings.Join(lines[:600], "") + " …\n"
	}
	return s
}

// ---------------------------------------------------------------- HTTP

func (s *Server) registerPxiClaudeRoutes() {
	h := func(p string, f http.HandlerFunc) { s.mux.HandleFunc(s.routePath(p), s.pxiTermLocal(f)) }
	h("/api/pxi/claude/accounts", s.handlePxiClaudeAccounts)
	h("/api/pxi/claude/account", s.handlePxiClaudeAccount)
	h("/api/pxi/claude/sessions", s.handlePxiClaudeSessions)
	h("/api/pxi/claude/history", s.handlePxiClaudeHistory)
	h("/api/pxi/claude/events", s.handlePxiClaudeEvents)
	h("/api/pxi/claude/watch", s.handlePxiClaudeWatch)
	h("/api/pxi/claude/status", s.handlePxiClaudeStatus)
	h("/api/pxi/claude/send", s.handlePxiClaudeSend)
	h("/api/pxi/claude/answer", s.handlePxiClaudeAnswer)
	h("/api/pxi/claude/control", s.handlePxiClaudeControl)
	h("/api/pxi/ide/context", s.handlePxiIDEContext)
	h("/api/pxi/ide/mention", s.handlePxiIDEMentionReq)
}

func (s *Server) handlePxiClaudeAccounts(w http.ResponseWriter, r *http.Request) {
	acc, ok := pxiClaudeAccountFor(s.ix.Root(), "")
	cur := ""
	if ok {
		cur = acc.ID
	}
	writeJSON(w, map[string]any{"accounts": pxiClaudeAccounts(), "current": cur, "installed": pxiClaudeBin() != ""})
}

func (s *Server) handlePxiClaudeAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Account string `json:"account"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	if _, ok := pxiClaudeAccountByID(b.Account); !ok {
		fail(w, http.StatusBadRequest, "unknown account")
		return
	}
	if err := pxiClaudeChoose(s.ix.Root(), b.Account); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// pxiClaudePlace resolves "" (the workspace) or a worktree name to a folder.
func (s *Server) pxiClaudePlace(where string) (string, bool) {
	if where == "" {
		return s.ix.Root(), true
	}
	wt := s.worktrees().lookup(where)
	if wt == nil || wt.Prunable {
		return "", false
	}
	return wt.Path, true
}

func (s *Server) handlePxiClaudeSessions(w http.ResponseWriter, r *http.Request) {
	acc, ok := pxiClaudeAccountFor(s.ix.Root(), r.URL.Query().Get("account"))
	if !ok {
		writeJSON(w, map[string]any{"places": []any{}})
		return
	}
	type place struct {
		Where    string             `json:"where"`
		Name     string             `json:"name"`
		Path     string             `json:"path"`
		Branch   string             `json:"branch,omitempty"`
		Sessions []pxiClaudeSession `json:"sessions"`
	}
	places := []place{{Where: "", Name: filepath.Base(s.ix.Root()), Path: s.ix.Root(), Sessions: pxiClaudeListSessions(acc, s.ix.Root())}}
	for _, wt := range s.worktrees().Snapshot(false) {
		if wt.Prunable {
			continue
		}
		places = append(places, place{Where: wt.Name, Name: wt.Name, Path: wt.Path, Branch: wt.Branch, Sessions: pxiClaudeListSessions(acc, wt.Path)})
	}
	m := pxiClaudeGet()
	m.mu.Lock()
	for i := range places {
		seen := map[string]bool{}
		for j := range places[i].Sessions {
			ss := &places[i].Sessions[j]
			seen[ss.ID] = true
			if run := m.runs[ss.ID]; run != nil {
				ss.Live, ss.Busy, ss.Asking = run.cmd != nil, run.Busy, run.Asking
			}
		}
		// A session started here that has not written its transcript yet.
		for _, run := range m.runs {
			if run.Account == acc.ID && run.Where == places[i].Where && !seen[run.Session] {
				places[i].Sessions = append([]pxiClaudeSession{{ID: run.Session, Title: run.Title, Updated: time.Now().UnixMilli(), Live: run.cmd != nil, Busy: run.Busy, Asking: run.Asking}}, places[i].Sessions...)
			}
		}
	}
	var terms []pxiClaudeSession
	for _, run := range m.runs {
		if run.ide != nil {
			terms = append(terms, pxiClaudeSession{ID: run.Session, Title: run.Title, Updated: time.Now().UnixMilli(), Live: true, Asking: run.Asking})
		}
	}
	m.mu.Unlock()
	if len(terms) > 0 {
		sort.Slice(terms, func(i, j int) bool { return terms[i].ID < terms[j].ID })
		places = append(places, place{Where: "@terminal", Name: "Claude in terminals", Sessions: terms})
	}
	writeJSON(w, map[string]any{"account": acc.ID, "places": places})
}

func (s *Server) handlePxiClaudeHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("session")
	if strings.HasPrefix(id, "ide-") {
		writeJSON(w, map[string]any{"events": []pxiClaudeEv{}})
		return
	}
	if !pxiClaudeIDRe.MatchString(id) {
		fail(w, http.StatusBadRequest, "bad session")
		return
	}
	acc, ok := pxiClaudeAccountFor(s.ix.Root(), q.Get("account"))
	cwd, okp := s.pxiClaudePlace(q.Get("where"))
	if !ok || !okp {
		fail(w, http.StatusBadRequest, "unknown account or worktree")
		return
	}
	m := pxiClaudeGet()
	m.mu.Lock()
	end := int64(-1)
	if run := m.runs[id]; run != nil {
		end = run.histEnd
	}
	m.mu.Unlock()
	events := pxiClaudeHistory(filepath.Join(pxiClaudeProjectDir(acc, cwd), id+".jsonl"), end)
	if events == nil {
		events = []pxiClaudeEv{}
	}
	writeJSON(w, map[string]any{"events": events})
}

func pxiSSE(w http.ResponseWriter) (http.Flusher, bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "streaming unsupported")
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	return fl, true
}

// handlePxiClaudeEvents streams one session: what this run has done so far,
// then live events. Open before the session runs, it waits for it.
func (s *Server) handlePxiClaudeEvents(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session")
	if !pxiClaudeAnyIDRe.MatchString(id) {
		fail(w, http.StatusBadRequest, "bad session")
		return
	}
	fl, ok := pxiSSE(w)
	if !ok {
		return
	}
	m := pxiClaudeGet()
	ch := make(chan pxiClaudeEv, 1024)
	m.mu.Lock()
	var replay []pxiClaudeEv
	if run := m.runs[id]; run != nil {
		replay = append(replay, run.events...)
		replay = append(replay, pxiClaudeEv{"k": "busy", "busy": run.Busy, "live": run.cmd != nil, "mode": run.Mode, "model": run.Model})
	} else {
		replay = append(replay, pxiClaudeEv{"k": "idle"})
	}
	if m.subs[id] == nil {
		m.subs[id] = map[chan pxiClaudeEv]struct{}{}
	}
	m.subs[id][ch] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if _, ok := m.subs[id][ch]; ok {
			delete(m.subs[id], ch)
			close(ch)
		}
		if len(m.subs[id]) == 0 {
			delete(m.subs, id)
		}
		m.mu.Unlock()
	}()
	send := func(ev pxiClaudeEv) bool {
		b, _ := json.Marshal(ev)
		_, err := fmt.Fprintf(w, "data: %s\n\n", b)
		return err == nil
	}
	fmt.Fprint(w, ": pxi claude\n\n")
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
		case ev, ok := <-ch:
			if !ok || !send(ev) {
				return
			}
			fl.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handlePxiClaudeStatus: whether anything runs (the page checks once on load).
func (s *Server) handlePxiClaudeStatus(w http.ResponseWriter, r *http.Request) {
	m := pxiClaudeGet()
	m.mu.Lock()
	runs, ide := 0, 0
	for _, run := range m.runs {
		if run.ide != nil {
			ide++
		} else {
			runs++
		}
	}
	m.mu.Unlock()
	writeJSON(w, map[string]any{"runs": runs, "ide": ide})
}

// handlePxiClaudeWatch: one tick whenever a session's status changes (the
// tab badge and session list refresh on it).
func (s *Server) handlePxiClaudeWatch(w http.ResponseWriter, r *http.Request) {
	fl, ok := pxiSSE(w)
	if !ok {
		return
	}
	m := pxiClaudeGet()
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	m.all[ch] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.all, ch)
		m.mu.Unlock()
	}()
	status := func() []byte {
		m.mu.Lock()
		defer m.mu.Unlock()
		list := make([]pxiClaudeRun, 0, len(m.runs))
		for _, run := range m.runs {
			list = append(list, pxiClaudeRun{Session: run.Session, Account: run.Account, Where: run.Where, Title: run.Title, Busy: run.Busy, Asking: run.Asking, Mode: run.Mode, Model: run.Model})
		}
		ide := 0
		for _, run := range m.runs {
			if run.ide != nil {
				ide++
			}
		}
		out := map[string]any{"runs": list, "ide": ide}
		if len(m.pageReqs) > 0 {
			out["page"] = m.pageReqs
			m.pageReqs = nil
		}
		b, _ := json.Marshal(out)
		return b
	}
	fmt.Fprintf(w, "data: %s\n\n", status())
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ch:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", status()); err != nil {
				return
			}
			fl.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handlePxiClaudeSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Account string `json:"account"`
		Where   string `json:"where"`
		Session string `json:"session"` // empty: a new session
		Text    string `json:"text"`
		Model   string `json:"model"`
		Mode    string `json:"mode"`
		Ctx     string `json:"ctx"` // what the attached context was, for the panel: "main.go:3-9"
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, pxiClaudeBodyMax)).Decode(&b); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	if strings.TrimSpace(b.Text) == "" {
		fail(w, http.StatusBadRequest, "empty message")
		return
	}
	if b.Mode != "" && !pxiClaudeModes[b.Mode] || !pxiClaudeModelRe.MatchString(b.Model) || strings.HasPrefix(b.Model, "-") {
		fail(w, http.StatusBadRequest, "bad model or mode")
		return
	}
	acc, ok := pxiClaudeAccountFor(s.ix.Root(), b.Account)
	if !ok {
		fail(w, http.StatusBadRequest, "no Claude account: run `claude` once in a terminal to log in")
		return
	}
	cwd, ok := s.pxiClaudePlace(b.Where)
	if !ok {
		fail(w, http.StatusBadRequest, "unknown worktree")
		return
	}
	resume := b.Session != ""
	if resume && !pxiClaudeIDRe.MatchString(b.Session) {
		fail(w, http.StatusBadRequest, "bad session")
		return
	}
	if !resume {
		b.Session = newUUID()
	}
	m := pxiClaudeGet()
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.runs[b.Session]
	if run == nil || run.stdin == nil {
		var err error
		run, err = m.startLocked(acc, cwd, b.Where, b.Session, resume, b.Model, b.Mode)
		if err != nil {
			fail(w, http.StatusConflict, err.Error())
			return
		}
	}
	if run.Title == "" {
		run.Title = pxiClaudeShort(pxiClaudeCleanUser(b.Text), 90)
	}
	msg := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": b.Text}}}}
	if err := run.writeLocked(msg); err != nil {
		fail(w, 500, "could not reach claude: "+err.Error())
		return
	}
	run.Busy = true
	if run.idle != nil {
		run.idle.Stop()
	}
	ev := pxiClaudeEv{"k": "user", "text": pxiClaudeCleanUser(b.Text)}
	if b.Ctx != "" {
		ev["ctx"] = pxiClaudeShort(b.Ctx, 200)
	}
	m.emitLocked(run, ev)
	m.emitLocked(run, pxiClaudeEv{"k": "busy", "busy": true, "live": true})
	m.changedLocked()
	writeJSON(w, map[string]any{"session": b.Session, "account": acc.ID, "where": b.Where})
}

// handlePxiClaudeAnswer answers a permission request: allow (optionally with
// changed input, such as answers to a question) or deny with a reason.
func (s *Server) handlePxiClaudeAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Session string         `json:"session"`
		Req     string         `json:"req"`
		Allow   bool           `json:"allow"`
		Message string         `json:"message"`
		Answers map[string]any `json:"answers"` // AskUserQuestion
		Mode    string         `json:"mode"`    // also switch mode, e.g. acceptEdits ("allow all edits")
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&b); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	if b.Mode != "" && !pxiClaudeModes[b.Mode] {
		fail(w, http.StatusBadRequest, "bad mode")
		return
	}
	m := pxiClaudeGet()
	m.mu.Lock()
	if run := m.runs[b.Session]; run != nil && run.ide != nil {
		c := run.ide
		m.mu.Unlock()
		if !pxiIDEAll.resolve(c, b.Req, &pxiIDEVerdict{allow: b.Allow, message: b.Message}) {
			fail(w, http.StatusNotFound, "already answered")
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	defer m.mu.Unlock()
	run := m.runs[b.Session]
	if run == nil {
		fail(w, http.StatusNotFound, "session is not running")
		return
	}
	pending, ok := run.asks[b.Req]
	if !ok {
		fail(w, http.StatusNotFound, "already answered")
		return
	}
	var resp map[string]any
	if b.Allow {
		input, _ := pending["input"].(map[string]any)
		if input == nil {
			input = map[string]any{}
		}
		if b.Answers != nil {
			input["answers"] = b.Answers
		}
		resp = map[string]any{"behavior": "allow", "updatedInput": input}
	} else {
		msg := strings.TrimSpace(b.Message)
		if msg == "" {
			msg = "The user rejected this in pxi."
		} else {
			msg = "The user rejected this in pxi and said: " + msg
		}
		resp = map[string]any{"behavior": "deny", "message": msg}
	}
	if b.Allow && b.Mode != "" {
		run.controlLocked(map[string]any{"subtype": "set_permission_mode", "mode": b.Mode})
		run.Mode = b.Mode
	}
	if err := run.writeLocked(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": b.Req, "response": resp}}); err != nil {
		fail(w, 500, err.Error())
		return
	}
	delete(run.asks, b.Req)
	run.Asking = len(run.asks)
	m.emitLocked(run, pxiClaudeEv{"k": "answered", "req": b.Req, "allow": b.Allow, "mode": run.Mode})
	m.changedLocked()
	writeJSON(w, map[string]any{"ok": true})
}

// handlePxiClaudeControl: interrupt the turn, change mode or model, or stop
// the session's process (and what it started).
func (s *Server) handlePxiClaudeControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Session string `json:"session"`
		Action  string `json:"action"` // interrupt | mode | model | stop
		Value   string `json:"value"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	m := pxiClaudeGet()
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.runs[b.Session]
	if run == nil || run.cmd == nil {
		writeJSON(w, map[string]any{"ok": true, "running": false})
		return
	}
	var err error
	switch b.Action {
	case "interrupt":
		err = run.controlLocked(map[string]any{"subtype": "interrupt"})
	case "mode":
		if !pxiClaudeModes[b.Value] {
			fail(w, http.StatusBadRequest, "bad mode")
			return
		}
		err = run.controlLocked(map[string]any{"subtype": "set_permission_mode", "mode": b.Value})
		run.Mode = b.Value
	case "model":
		if !pxiClaudeModelRe.MatchString(b.Value) || strings.HasPrefix(b.Value, "-") {
			fail(w, http.StatusBadRequest, "bad model")
			return
		}
		v := any(b.Value)
		if b.Value == "" {
			v = nil
		}
		err = run.controlLocked(map[string]any{"subtype": "set_model", "model": v})
		run.Model = b.Value
	case "stop":
		if run.stdin != nil {
			run.stdin.Close()
			run.stdin = nil
		}
		pid := run.cmd.Process.Pid
		syscall.Kill(-pid, syscall.SIGTERM)
		go func() {
			time.Sleep(3 * time.Second)
			syscall.Kill(-pid, syscall.SIGKILL)
		}()
	default:
		fail(w, http.StatusBadRequest, "unknown action")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	m.changedLocked()
	writeJSON(w, map[string]any{"ok": true, "running": true})
}
