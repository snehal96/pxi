//go:build !windows

package main

// Claude Code IDE bridge: `claude` running in pxi's terminal (or started
// anywhere and connected with /ide) talks to pxi the way it talks to VS Code.
// Its proposed edits appear in the Claude tab as the same Accept / Reject
// diff cards the panel uses, and it can see the file and selection open in
// pxi.
//
// Transport: a WebSocket server on 127.0.0.1 speaking MCP (JSON-RPC 2.0),
// announced by a lock file ~/.claude/ide/<port>.lock (read by every account),
// holding a random token the client must send. pxi's terminals get
// CLAUDE_CODE_SSE_PORT and ENABLE_IDE_INTEGRATION so `claude` connects by
// itself. The protocol is Claude Code's own and not publicly documented.
//
// Rule zero: nothing listens until the first terminal opens; the lock files
// go away when pxi exits.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type pxiIDE struct {
	s     *Server
	port  int
	token string
	locks []string

	mu      sync.Mutex
	next    int
	clients map[int]*pxiIDEClient
	sel     map[string]any // latest selection_changed params, from the page
	open    []map[string]any
}

type pxiIDEClient struct {
	id      int
	ws      *pxiWS
	name    string // the client's name and version from initialize
	pid     int    // the claude process (ide_connected), for the process list
	pending map[string]chan pxiIDEVerdict
}

type pxiIDEVerdict struct {
	allow   bool
	message string
}

var pxiIDEOnce sync.Once
var pxiIDEAll *pxiIDE
var pxiIDEErr error

// pxiIDEStart starts the bridge once; later calls return it.
func (s *Server) pxiIDEStart() (*pxiIDE, error) {
	pxiIDEOnce.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			pxiIDEErr = err
			return
		}
		b := make([]byte, 24)
		rand.Read(b)
		ide := &pxiIDE{s: s, port: ln.Addr().(*net.TCPAddr).Port, token: hex.EncodeToString(b), clients: map[int]*pxiIDEClient{}}
		srv := &http.Server{Handler: http.HandlerFunc(ide.serve), ReadHeaderTimeout: 10 * time.Second}
		go srv.Serve(ln)
		ide.writeLocks()
		pxiIDEAll = ide
	})
	return pxiIDEAll, pxiIDEErr
}

// TermEnv is what a pxi terminal needs for `claude` to connect by itself.
func (ide *pxiIDE) TermEnv() []string {
	return []string{"CLAUDE_CODE_SSE_PORT=" + strconv.Itoa(ide.port), "ENABLE_IDE_INTEGRATION=true"}
}

func (ide *pxiIDE) folders() []string {
	out := []string{ide.s.ix.Root()}
	for _, wt := range ide.s.worktrees().Snapshot(false) {
		if !wt.Prunable {
			out = append(out, wt.Path)
		}
	}
	return out
}

func (ide *pxiIDE) writeLocks() {
	lock, _ := json.Marshal(map[string]any{
		"pid": os.Getpid(), "workspaceFolders": ide.folders(), "ideName": "pxi",
		"transport": "ws", "runningInWindows": false, "authToken": ide.token,
	})
	// Claude Code reads ~/.claude/ide whichever account (CLAUDE_CONFIG_DIR) it
	// runs with, so one lock file serves them all; a second copy would make it
	// see pxi twice and connect to neither.
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".claude", "ide")
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	p := filepath.Join(dir, strconv.Itoa(ide.port)+".lock")
	if os.WriteFile(p, lock, 0o600) == nil {
		ide.locks = append(ide.locks, p)
	}
}

func (ide *pxiIDE) removeLocks() {
	for _, p := range ide.locks {
		os.Remove(p)
	}
}

// pxiIDEStop removes the lock files (main.go: on shutdown).
func pxiIDEStop() {
	if pxiIDEAll != nil {
		pxiIDEAll.removeLocks()
	}
}

func (ide *pxiIDE) serve(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("PXI_IDE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "ide: %s %s origin=%q auth=%v upgrade=%q proto=%q\n", r.Method, r.URL.Path, r.Header.Get("Origin"), r.Header.Get("x-claude-code-ide-authorization") == ide.token, r.Header.Get("Upgrade"), r.Header.Get("Sec-WebSocket-Protocol"))
	}
	// Only Claude Code: the right token, and never a web page (browsers
	// always send an Origin on WebSocket requests).
	if r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("x-claude-code-ide-authorization")), []byte(ide.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	ws, err := pxiWSUpgrade(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ide.mu.Lock()
	ide.next++
	c := &pxiIDEClient{id: ide.next, ws: ws, pending: map[string]chan pxiIDEVerdict{}}
	ide.clients[c.id] = c
	ide.mu.Unlock()
	ide.register(c)
	defer func() {
		ws.Close()
		ide.mu.Lock()
		delete(ide.clients, c.id)
		for _, ch := range c.pending {
			close(ch)
		}
		ide.mu.Unlock()
		ide.unregister(c)
	}()
	for {
		msg, err := ws.ReadMessage()
		if err != nil {
			return
		}
		go ide.handle(c, msg)
	}
}

type pxiRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (c *pxiIDEClient) reply(id json.RawMessage, result any, rpcErr *string) {
	if len(id) == 0 {
		return
	}
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		m["error"] = map[string]any{"code": -32601, "message": *rpcErr}
	} else {
		m["result"] = result
	}
	b, _ := json.Marshal(m)
	c.ws.WriteText(b)
}

func (c *pxiIDEClient) notify(method string, params any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	c.ws.WriteText(b)
}

func pxiMCPText(texts ...string) map[string]any {
	content := make([]any, len(texts))
	for i, t := range texts {
		content[i] = map[string]any{"type": "text", "text": t}
	}
	return map[string]any{"content": content}
}

func pxiMCPJSON(v any) map[string]any {
	b, _ := json.Marshal(v)
	return pxiMCPText(string(b))
}

var pxiIDETools = []map[string]any{
	{"name": "openFile", "description": "Open a file in pxi", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"filePath": map[string]any{"type": "string"}, "startText": map[string]any{"type": "string"}, "endText": map[string]any{"type": "string"}}, "required": []string{"filePath"}}},
	{"name": "openDiff", "description": "Show a proposed change in pxi for the user to accept or reject", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"old_file_path": map[string]any{"type": "string"}, "new_file_path": map[string]any{"type": "string"}, "new_file_contents": map[string]any{"type": "string"}, "tab_name": map[string]any{"type": "string"}}, "required": []string{"old_file_path", "new_file_path", "new_file_contents", "tab_name"}}},
	{"name": "getCurrentSelection", "description": "The selection in pxi's active editor", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "getLatestSelection", "description": "The most recent selection in pxi", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "getOpenEditors", "description": "Files open in pxi", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "getWorkspaceFolders", "description": "The repository and its worktrees", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
	{"name": "getDiagnostics", "description": "Diagnostics (pxi reports none)", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"uri": map[string]any{"type": "string"}}}},
	{"name": "checkDocumentDirty", "description": "Whether a file has unsaved changes", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"filePath": map[string]any{"type": "string"}}, "required": []string{"filePath"}}},
	{"name": "saveDocument", "description": "Save a file", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"filePath": map[string]any{"type": "string"}}, "required": []string{"filePath"}}},
	{"name": "close_tab", "description": "Close a diff", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"tab_name": map[string]any{"type": "string"}}, "required": []string{"tab_name"}}},
	{"name": "closeAllDiffTabs", "description": "Close all diffs", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
}

func (ide *pxiIDE) handle(c *pxiIDEClient, raw []byte) {
	var m pxiRPC
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	if os.Getenv("PXI_IDE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "ide rpc: %.300s\n", raw)
	}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		json.Unmarshal(m.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-03-26"
		}
		ide.mu.Lock()
		c.name = strings.TrimSpace(p.ClientInfo.Name + " " + p.ClientInfo.Version)
		ide.mu.Unlock()
		c.reply(m.ID, map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}, "logging": map[string]any{}},
			"serverInfo":      map[string]any{"name": "pxi", "version": version},
		}, nil)
	case "ide_connected":
		var p struct {
			Pid int `json:"pid"`
		}
		json.Unmarshal(m.Params, &p)
		ide.mu.Lock()
		c.pid = p.Pid
		ide.mu.Unlock()
	case "notifications/initialized", "notifications/cancelled":
	case "ping":
		c.reply(m.ID, map[string]any{}, nil)
	case "tools/list":
		c.reply(m.ID, map[string]any{"tools": pxiIDETools}, nil)
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		json.Unmarshal(m.Params, &p)
		c.reply(m.ID, ide.call(c, p.Name, p.Arguments), nil)
	case "prompts/list":
		c.reply(m.ID, map[string]any{"prompts": []any{}}, nil)
	case "resources/list":
		c.reply(m.ID, map[string]any{"resources": []any{}}, nil)
	default:
		msg := "method not found: " + m.Method
		c.reply(m.ID, nil, &msg)
	}
}

func pxiArg(a map[string]any, k string) string { v, _ := a[k].(string); return v }

func (ide *pxiIDE) call(c *pxiIDEClient, name string, a map[string]any) any {
	switch name {
	case "openDiff":
		return ide.openDiff(c, a)
	case "openFile":
		p := pxiArg(a, "filePath")
		ide.page(map[string]any{"open": p, "find": pxiArg(a, "startText")})
		return pxiMCPText("Opened file: " + p)
	case "getCurrentSelection", "getLatestSelection":
		ide.mu.Lock()
		sel := ide.sel
		ide.mu.Unlock()
		if sel == nil {
			return pxiMCPJSON(map[string]any{"success": false, "message": "No active editor found"})
		}
		out := map[string]any{"success": true}
		for k, v := range sel {
			out[k] = v
		}
		return pxiMCPJSON(out)
	case "getOpenEditors":
		ide.mu.Lock()
		tabs := ide.open
		ide.mu.Unlock()
		if tabs == nil {
			tabs = []map[string]any{}
		}
		return pxiMCPJSON(map[string]any{"tabs": tabs})
	case "getWorkspaceFolders":
		var folders []map[string]any
		for _, f := range ide.folders() {
			folders = append(folders, map[string]any{"name": filepath.Base(f), "uri": "file://" + f, "path": f})
		}
		return pxiMCPJSON(map[string]any{"success": true, "folders": folders, "rootPath": ide.s.ix.Root()})
	case "getDiagnostics":
		return pxiMCPJSON([]any{})
	case "checkDocumentDirty":
		return pxiMCPJSON(map[string]any{"success": true, "filePath": pxiArg(a, "filePath"), "isDirty": false, "isUntitled": false})
	case "saveDocument":
		return pxiMCPJSON(map[string]any{"success": true, "filePath": pxiArg(a, "filePath"), "saved": true, "message": "Document saved"})
	case "close_tab":
		// Answered in the terminal: the card is no longer needed.
		ide.resolve(c, pxiArg(a, "tab_name"), nil)
		return pxiMCPText("TAB_CLOSED")
	case "closeAllDiffTabs":
		ide.mu.Lock()
		names := make([]string, 0, len(c.pending))
		for n := range c.pending {
			names = append(names, n)
		}
		ide.mu.Unlock()
		for _, n := range names {
			ide.resolve(c, n, nil)
		}
		return pxiMCPText(fmt.Sprintf("CLOSED_%d_DIFF_TABS", len(names)))
	}
	return pxiMCPText("Tool not supported by pxi: " + name)
}

// openDiff shows the change as a card in the Claude tab and waits for the
// user. Claude Code itself writes the file once the change is accepted.
func (ide *pxiIDE) openDiff(c *pxiIDEClient, a map[string]any) any {
	oldPath, newPath, contents, tab := pxiArg(a, "old_file_path"), pxiArg(a, "new_file_path"), pxiArg(a, "new_file_contents"), pxiArg(a, "tab_name")
	if tab == "" {
		tab = newPath
	}
	old := ""
	if b, err := os.ReadFile(oldPath); err == nil && len(b) < 8<<20 {
		old = string(b)
	}
	ch := make(chan pxiIDEVerdict, 1)
	ide.mu.Lock()
	c.pending[tab] = ch
	ide.mu.Unlock()
	tool := "Edit"
	if old == "" {
		tool = "Write"
	}
	ide.ask(c, tab, pxiClaudeEv{"k": "ask", "req": tab, "tool": tool, "label": newPath,
		"input": map[string]any{"file_path": newPath}, "diff": pxiUnifiedDiff(old, contents)})
	v, ok := <-ch
	if !ok {
		return pxiMCPText("DIFF_REJECTED", tab)
	}
	if v.allow {
		return pxiMCPText("FILE_SAVED", contents)
	}
	return pxiMCPText("DIFF_REJECTED", tab)
}

// resolve answers a pending diff; v nil means it was settled elsewhere.
func (ide *pxiIDE) resolve(c *pxiIDEClient, tab string, v *pxiIDEVerdict) bool {
	ide.mu.Lock()
	ch, ok := c.pending[tab]
	if ok {
		delete(c.pending, tab)
	}
	ide.mu.Unlock()
	if !ok {
		return false
	}
	if v != nil {
		ch <- *v
	}
	close(ch)
	allow := v != nil && v.allow
	ide.answered(c, tab, allow, v == nil)
	return true
}

// ---------------------------------------------------------------- the Claude tab

// A connected client appears in the Claude tab as a session "ide-<n>" with
// no transcript of its own: only the diffs it proposes.
func pxiIDESession(id int) string { return "ide-" + strconv.Itoa(id) }

func (ide *pxiIDE) register(c *pxiIDEClient) {
	m := pxiClaudeGet()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[pxiIDESession(c.id)] = &pxiClaudeRun{Session: pxiIDESession(c.id), Where: "@terminal", Title: "claude in a terminal", ide: c, asks: map[string]pxiClaudeEv{}}
	m.changedLocked()
}

func (ide *pxiIDE) unregister(c *pxiIDEClient) {
	m := pxiClaudeGet()
	m.mu.Lock()
	defer m.mu.Unlock()
	if run := m.runs[pxiIDESession(c.id)]; run != nil {
		for id := range run.asks {
			m.emitLocked(run, pxiClaudeEv{"k": "answered", "req": id, "allow": false})
		}
		m.emitLocked(run, pxiClaudeEv{"k": "exit", "code": 0})
		delete(m.runs, pxiIDESession(c.id))
	}
	m.changedLocked()
}

func (ide *pxiIDE) ask(c *pxiIDEClient, tab string, ev pxiClaudeEv) {
	m := pxiClaudeGet()
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.runs[pxiIDESession(c.id)]
	if run == nil {
		return
	}
	if lbl, _ := ev["label"].(string); lbl != "" {
		run.Title = "claude in a terminal · " + filepath.Base(lbl)
	}
	run.asks[tab] = pxiClaudeEv{"ev": ev}
	run.Asking = len(run.asks)
	m.emitLocked(run, ev)
	m.changedLocked()
}

func (ide *pxiIDE) answered(c *pxiIDEClient, tab string, allow, elsewhere bool) {
	m := pxiClaudeGet()
	m.mu.Lock()
	defer m.mu.Unlock()
	run := m.runs[pxiIDESession(c.id)]
	if run == nil {
		return
	}
	delete(run.asks, tab)
	run.Asking = len(run.asks)
	ev := pxiClaudeEv{"k": "answered", "req": tab, "allow": allow}
	if elsewhere {
		ev["elsewhere"] = true
	}
	m.emitLocked(run, ev)
	m.changedLocked()
}

// page sends a request to the open pxi pages (open a file) through the
// Claude status stream.
func (ide *pxiIDE) page(v map[string]any) {
	m := pxiClaudeGet()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pageReqs = append(m.pageReqs, v)
	if len(m.pageReqs) > 8 {
		m.pageReqs = m.pageReqs[len(m.pageReqs)-8:]
	}
	m.changedLocked()
}

// handlePxiIDEContext: the page reports the active file and selection, which
// connected clients receive as selection_changed (only sent while a client
// is connected; the page learns that from the status stream).
func (s *Server) handlePxiIDEContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Path string `json:"path"` // pxi path
		Text string `json:"text"`
		L1   int    `json:"l1"`
		L2   int    `json:"l2"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&b); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	ide := pxiIDEAll
	if ide == nil {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	abs := ""
	if b.Path != "" {
		if a, _, ok := s.resolvePath(b.Path); ok {
			abs = a
		}
	}
	var sel map[string]any
	if abs != "" {
		line := func(n int) int { return max(0, n-1) }
		sel = map[string]any{
			"text": b.Text, "filePath": abs, "fileUrl": "file://" + abs,
			"selection": map[string]any{
				"start":   map[string]any{"line": line(b.L1), "character": 0},
				"end":     map[string]any{"line": line(b.L2), "character": 0},
				"isEmpty": b.Text == "",
			},
		}
	}
	ide.mu.Lock()
	ide.sel = sel
	if abs != "" {
		ide.open = []map[string]any{{"uri": "file://" + abs, "isActive": true, "label": filepath.Base(abs), "isDirty": false}}
	}
	clients := make([]*pxiIDEClient, 0, len(ide.clients))
	for _, c := range ide.clients {
		clients = append(clients, c)
	}
	ide.mu.Unlock()
	if sel != nil {
		for _, c := range clients {
			c.notify("selection_changed", sel)
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

// pxiIDEMention sends a file and lines to every connected client, which adds
// them to the prompt being typed (Claude Code's @-mention from the IDE).
func pxiIDEMention(abs string, l1, l2 int) int {
	ide := pxiIDEAll
	if ide == nil {
		return 0
	}
	ide.mu.Lock()
	clients := make([]*pxiIDEClient, 0, len(ide.clients))
	for _, c := range ide.clients {
		clients = append(clients, c)
	}
	ide.mu.Unlock()
	params := map[string]any{"filePath": abs}
	if l1 > 0 {
		params["lineStart"] = l1 - 1
		params["lineEnd"] = max(l1, l2) - 1
	}
	for _, c := range clients {
		c.notify("at_mentioned", params)
	}
	return len(clients)
}

// handlePxiIDEMentionReq: "Mention in terminal Claude" on a file and lines.
func (s *Server) handlePxiIDEMentionReq(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Path string `json:"path"`
		L1   int    `json:"l1"`
		L2   int    `json:"l2"`
	}
	if !pxiReadBody(w, r, &b) {
		return
	}
	abs, _, ok := s.resolvePath(b.Path)
	if !ok {
		fail(w, http.StatusBadRequest, "bad path")
		return
	}
	writeJSON(w, map[string]any{"sent": pxiIDEMention(abs, b.L1, b.L2)})
}
