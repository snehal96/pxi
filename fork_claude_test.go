//go:build !windows

package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stand-in for `claude -p --input-format stream-json`: on the first message
// it asks to edit a.txt, then reports what pxi answered and ends the turn.
const fakeClaude = `#!/bin/sh
echo '{"type":"system","subtype":"init","model":"fake-model","permissionMode":"default"}'
read -r msg
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Editing."},{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"'"$PWD"'/a.txt"}}]}}'
echo '{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Edit","tool_use_id":"t1","input":{"file_path":"'"$PWD"'/a.txt","old_string":"hi","new_string":"hello","replace_all":false}}}'
read -r answer
case "$answer" in *'"behavior":"allow"'*) v=allowed ;; *) v=denied ;; esac
echo '{"type":"assistant","message":{"content":[{"type":"text","text":"'$v'"}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"duration_ms":5,"num_turns":1}'
read -r more
`

func TestPxiClaudeSessionAsksAndApplies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	acc := filepath.Join(home, ".claude-test")
	os.MkdirAll(acc, 0o755)
	os.WriteFile(filepath.Join(acc, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"a@b.c","organizationName":"Org"}}`), 0o600)
	bin := filepath.Join(home, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	s, root := newTestServer(t)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi\n"), 0o644)
	srv := httptest.NewServer(hostGuard(s))
	defer srv.Close()
	post := func(path, body string) map[string]any {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		req.Header.Set("Origin", srv.URL)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		json.NewDecoder(res.Body).Decode(&m)
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d %v", path, res.StatusCode, m)
		}
		return m
	}

	res, err := http.Get(srv.URL + "/api/pxi/claude/accounts")
	if err != nil {
		t.Fatal(err)
	}
	var accs struct {
		Accounts []pxiClaudeAccount `json:"accounts"`
		Current  string             `json:"current"`
	}
	json.NewDecoder(res.Body).Decode(&accs)
	res.Body.Close()
	if accs.Current != ".claude-test" || len(accs.Accounts) != 1 || accs.Accounts[0].Email != "a@b.c" {
		t.Fatalf("accounts: %+v", accs)
	}

	sent := post("/api/pxi/claude/send", `{"text":"change it","model":"haiku"}`)
	id, _ := sent["session"].(string)
	if !pxiClaudeIDRe.MatchString(id) {
		t.Fatalf("send: %v", sent)
	}
	ev, err := http.Get(srv.URL + "/api/pxi/claude/events?session=" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer ev.Body.Close()
	events := make(chan pxiClaudeEv, 64)
	go func() {
		sc := bufio.NewScanner(ev.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if line, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var e pxiClaudeEv
				if json.Unmarshal([]byte(line), &e) == nil {
					events <- e
				}
			}
		}
		close(events)
	}()
	wait := func(k string) pxiClaudeEv {
		deadline := time.After(5 * time.Second)
		for {
			select {
			case e, ok := <-events:
				if !ok {
					t.Fatalf("stream ended waiting for %s", k)
				}
				if e["k"] == k {
					return e
				}
			case <-deadline:
				t.Fatalf("no %s event", k)
			}
		}
	}
	ask := wait("ask")
	if ask["tool"] != "Edit" || !strings.Contains(ask["diff"].(string), "-hi\n+hello") {
		t.Fatalf("ask: %v", ask)
	}
	post("/api/pxi/claude/answer", `{"session":"`+id+`","req":"r1","allow":true}`)
	if e := wait("answered"); e["allow"] != true {
		t.Fatalf("answered: %v", e)
	}
	if e := wait("text"); e["text"] != "allowed" {
		t.Fatalf("claude saw: %v", e)
	}
	wait("done")
	post("/api/pxi/claude/control", `{"session":"`+id+`","action":"stop"}`)
	wait("exit")
}

func TestPxiClaudeRefusesOtherMachines(t *testing.T) {
	s, _ := newTestServer(t)
	h := hostGuard(s)
	rec := serve(h, "POST", "/api/pxi/claude/send", "192.168.1.20:7777", "http://192.168.1.20:7777", `{"text":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("LAN host: want 403, got %d", rec.Code)
	}
	rec = serve(h, "POST", "/api/pxi/claude/send", "127.0.0.1:7777", "http://evil.example", `{"text":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site: want 403, got %d", rec.Code)
	}
}

func TestPxiClaudeTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"<ide_selection>The user selected x</ide_selection>\nfix the bug"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Looking."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test","description":"Run tests"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":"subagent"}}`,
		`{"type":"ai-title","aiTitle":"Fix the bug"}`,
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	evs := pxiClaudeHistory(path, -1)
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e["k"].(string))
	}
	if got := strings.Join(kinds, ","); got != "user,text,tool,result" {
		t.Fatalf("events: %s %v", got, evs)
	}
	if evs[0]["text"] != "fix the bug" || evs[2]["label"] != "Run tests" {
		t.Errorf("content: %v", evs)
	}
	if got := pxiClaudeTitle(path); got != "Fix the bug" {
		t.Errorf("title: %q", got)
	}
	if got := pxiClaudeProjectDir(pxiClaudeAccount{dir: "/h/.claude"}, "/no/such/my.repo"); got != "/h/.claude/projects/-no-such-my-repo" {
		t.Errorf("project dir: %s", got)
	}
}
