//go:build !windows

package main

import "testing"

func TestPxiProcLabel(t *testing.T) {
	cmd := `/bin/zsh -c source /h/.claude/shell-snapshots/snapshot-zsh-1.sh 2>/dev/null || true && eval 'npm run dev -- --port 3000' < /dev/null && pwd -P >| /tmp/claude-1-cwd`
	if got := pxiProcLabel(cmd); got != "$ npm run dev -- --port 3000" {
		t.Errorf("label: %q", got)
	}
	if got := pxiProcLabel(`/bin/zsh -c source /h/.claude/shell-snapshots/s.sh && eval 'echo '\''hi'\''' < /dev/null`); got != "$ echo 'hi'" {
		t.Errorf("quoted: %q", got)
	}
	if pxiProcLabel("node server.js") != "" {
		t.Error("plain command got a label")
	}
}
