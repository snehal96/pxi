//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A home with three Claude folders: two named by shell aliases, one logged out.
func pxiHarnessHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "bin")
	os.MkdirAll(bin, 0o755)
	// claude auth status: logged in unless the folder holds "out".
	script := `#!/bin/sh
d="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
if [ -f "$d/out" ]; then echo '{"loggedIn": false}'; exit 1; fi
echo "{\"loggedIn\": true, \"email\": \"$(basename $d)@x\", \"orgName\": \"o\", \"subscriptionType\": \"team\"}"
`
	os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, d := range []string{".claude", ".claude-work", ".claude-home", ".claude-old"} {
		os.MkdirAll(filepath.Join(home, d), 0o700)
	}
	os.WriteFile(filepath.Join(home, ".claude-old", "out"), nil, 0o600)
	os.WriteFile(filepath.Join(home, ".zshrc"), []byte(`
alias claude-office='CLAUDE_CONFIG_DIR=~/.claude-work /usr/local/bin/claude'
alias ch="CLAUDE_CONFIG_DIR=$HOME/.claude-home claude --model opus"
alias claude="echo use specific commands"
`), 0o644)
	pxiHarnessAccMu.Lock()
	pxiHarnessAccList, pxiHarnessAccAt = nil, time.Time{}
	pxiHarnessAccMu.Unlock()
	return home
}

func TestPxiHarnessAccountsFromAliasesAndFolders(t *testing.T) {
	home := pxiHarnessHome(t)
	list := pxiHarnessAccounts()
	var got []string
	for _, a := range list {
		got = append(got, a.Label+"="+a.Email)
	}
	want := "ch=.claude-home@x claude-office=.claude-work@x claude=.claude@x"
	if strings.Join(got, " ") != want {
		t.Fatalf("accounts:\n got %s\nwant %s", strings.Join(got, " "), want)
	}

	// No choice: the first one. A thread keeps its account after the choice changes.
	if env := pxiHarnessEnv("claude", "/r", "t1"); len(env) != 1 || env[0] != "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude-home") {
		t.Errorf("first account: %v", env)
	}
	c := pxiHarnessRead()
	c.Account = ".claude-work"
	pxiHarnessWrite(c)
	if env := pxiHarnessEnv("claude", "/r", "t2"); env[0] != "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude-work") {
		t.Errorf("chosen account: %v", env)
	}
	if env := pxiHarnessEnv("claude", "/r", "t1"); env[0] != "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude-home") {
		t.Errorf("thread t1 must keep its account: %v", env)
	}
	c = pxiHarnessRead()
	c.Account = ".claude"
	pxiHarnessWrite(c)
	if env := pxiHarnessEnv("claude", "/r", ""); env != nil {
		t.Errorf("default account needs no CLAUDE_CONFIG_DIR: %v", env)
	}
	if env := pxiHarnessEnv("codex", "/r", ""); env != nil {
		t.Errorf("other harnesses untouched: %v", env)
	}
}
