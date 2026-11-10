//go:build !windows

package main

// Claude accounts as choices for px0's `claude` harness (Threads, AI edits,
// commit messages): one entry per account that is logged in, named after the
// shell alias that runs it when there is one (claude-work, claude-personal).
//
// Accounts come from the aliases in the shell's startup files
// (alias claude-x='CLAUDE_CONFIG_DIR=~/.claude-x claude'), from ~/.claude-*
// folders and from the default ~/.claude. Each is checked once with
// `claude auth status`, which reads the login without starting a session or
// using any of the plan. The list is built the first time it is asked for and
// kept for ten minutes.
//
// The choice is one for all repositories (settings dir, claude-harness.json).
// A thread keeps the account it started with: Claude Code can only resume a
// session from the account that holds it.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/px0-ai/harness"
)

type pxiHarnessAccount struct {
	ID    string `json:"id"`    // config folder name: ".claude", ".claude-work"
	Label string `json:"label"` // what the menu shows: the alias, else claude-<folder>
	Email string `json:"email,omitempty"`
	Org   string `json:"org,omitempty"`
	Plan  string `json:"plan,omitempty"`
	dir   string
}

var (
	pxiHarnessAccMu   sync.Mutex
	pxiHarnessAccList []pxiHarnessAccount
	pxiHarnessAccAt   time.Time
	pxiHarnessFileMu  sync.Mutex
)

const pxiHarnessThreadCap = 500 // threads remembered with their account

// alias NAME='CLAUDE_CONFIG_DIR=DIR ... claude ...'
var pxiAliasRe = regexp.MustCompile(`^\s*alias\s+([\w.-]+)=["']?\s*(?:env\s+)?CLAUDE_CONFIG_DIR=["']?([^\s"']+)["']?\s+[^"']*\bclaude\b`)

// pxiShellAliases maps a config folder to the alias that runs claude with it.
func pxiShellAliases(home string) map[string]string {
	out := map[string]string{}
	for _, f := range []string{".zshrc", ".zprofile", ".zshenv", ".bashrc", ".bash_profile", ".bash_aliases", ".aliases"} {
		fh, err := os.Open(filepath.Join(home, f))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			m := pxiAliasRe.FindStringSubmatch(sc.Text())
			if m == nil {
				continue
			}
			dir := m[2]
			dir = strings.Replace(dir, "$HOME", home, 1)
			dir = strings.Replace(dir, "${HOME}", home, 1)
			if strings.HasPrefix(dir, "~/") {
				dir = filepath.Join(home, dir[2:])
			}
			dir = filepath.Clean(dir)
			if _, seen := out[dir]; !seen {
				out[dir] = m[1]
			}
		}
		fh.Close()
	}
	return out
}

// pxiAuthStatus asks claude whether the account in dir is logged in.
func pxiAuthStatus(bin, dir, home string) (email, org, plan string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status")
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k != "CLAUDE_CONFIG_DIR" && k != "CLAUDECODE" && k != "CLAUDE_CODE_ENTRYPOINT" {
			env = append(env, kv)
		}
	}
	if dir != filepath.Join(home, ".claude") {
		env = append(env, "CLAUDE_CONFIG_DIR="+dir)
	}
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return "", "", "", false
	}
	var j struct {
		LoggedIn bool   `json:"loggedIn"`
		Email    string `json:"email"`
		Org      string `json:"orgName"`
		Plan     string `json:"subscriptionType"`
	}
	if json.Unmarshal(out, &j) != nil || !j.LoggedIn {
		return "", "", "", false
	}
	return j.Email, j.Org, j.Plan, true
}

// pxiHarnessAccounts lists the logged-in accounts, aliased ones first in the
// order the shell files name them.
func pxiHarnessAccounts() []pxiHarnessAccount {
	pxiHarnessAccMu.Lock()
	defer pxiHarnessAccMu.Unlock()
	if pxiHarnessAccList != nil && time.Since(pxiHarnessAccAt) < 10*time.Minute {
		return pxiHarnessAccList
	}
	home, err := os.UserHomeDir()
	bin, found := harness.LookPath("claude")
	if err != nil || !found {
		pxiHarnessAccList, pxiHarnessAccAt = []pxiHarnessAccount{}, time.Now()
		return pxiHarnessAccList
	}
	aliases := pxiShellAliases(home)
	var cands []pxiHarnessAccount
	seen := map[string]bool{}
	add := func(dir string) {
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		id := filepath.Base(dir)
		label := aliases[dir]
		if label == "" {
			if id == ".claude" {
				label = "claude"
			} else {
				label = "claude-" + strings.TrimPrefix(id, ".claude-")
			}
		}
		cands = append(cands, pxiHarnessAccount{ID: id, Label: label, dir: dir})
	}
	// Aliased folders first, in a stable order.
	var aliased []string
	for dir := range aliases {
		aliased = append(aliased, dir)
	}
	sortStrings(aliased, func(a, b string) bool { return aliases[a] < aliases[b] })
	for _, dir := range aliased {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			add(dir)
		}
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".claude-") {
			add(filepath.Join(home, e.Name()))
		}
	}
	add(filepath.Join(home, ".claude"))

	var wg sync.WaitGroup
	ok := make([]bool, len(cands))
	for i := range cands {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := &cands[i]
			c.Email, c.Org, c.Plan, ok[i] = pxiAuthStatus(bin, c.dir, home)
		}(i)
	}
	wg.Wait()
	list := []pxiHarnessAccount{}
	for i, c := range cands {
		if ok[i] {
			list = append(list, c)
		}
	}
	pxiHarnessAccList, pxiHarnessAccAt = list, time.Now()
	return list
}

func sortStrings(s []string, less func(a, b string) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

type pxiHarnessChoice struct {
	Account string            `json:"account,omitempty"`
	Threads map[string]string `json:"threads,omitempty"` // thread id -> account id
	Order   []string          `json:"order,omitempty"`   // thread ids, oldest first, to bound Threads
}

func pxiHarnessChoicePath() string {
	return filepath.Join(filepath.Dir(pxiClaudeChoicePath()), "claude-harness.json")
}

func pxiHarnessRead() pxiHarnessChoice {
	var c pxiHarnessChoice
	if b, err := os.ReadFile(pxiHarnessChoicePath()); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

func pxiHarnessWrite(c pxiHarnessChoice) {
	b, _ := json.MarshalIndent(c, "", "  ")
	os.MkdirAll(filepath.Dir(pxiHarnessChoicePath()), 0o700)
	os.WriteFile(pxiHarnessChoicePath(), b, 0o600)
}

// pxiHarnessSelected: the chosen account if it is still logged in, else the
// first one listed.
func pxiHarnessSelected(list []pxiHarnessAccount, id string) (pxiHarnessAccount, bool) {
	for _, a := range list {
		if a.ID == id {
			return a, true
		}
	}
	if len(list) > 0 {
		return list[0], true
	}
	return pxiHarnessAccount{}, false
}

// pxiHarnessEnv is the environment px0's harness runs get (agent.go): for
// `claude`, the chosen account; a thread keeps the account of its first turn.
func pxiHarnessEnv(name, root, tid string) []string {
	if name != "claude" {
		return nil
	}
	list := pxiHarnessAccounts()
	pxiHarnessFileMu.Lock()
	c := pxiHarnessRead()
	id := c.Account
	if tid != "" {
		if t, ok := c.Threads[tid]; ok {
			id = t
		}
	}
	acc, ok := pxiHarnessSelected(list, id)
	if ok && tid != "" && c.Threads[tid] == "" {
		if c.Threads == nil {
			c.Threads = map[string]string{}
		}
		c.Threads[tid] = acc.ID
		c.Order = append(c.Order, tid)
		for len(c.Order) > pxiHarnessThreadCap {
			delete(c.Threads, c.Order[0])
			c.Order = c.Order[1:]
		}
		pxiHarnessWrite(c)
	}
	pxiHarnessFileMu.Unlock()
	if !ok || acc.ID == ".claude" {
		return nil
	}
	return []string{"CLAUDE_CONFIG_DIR=" + acc.dir}
}

// handlePxiHarnessAccounts: GET lists the accounts and the choice; POST
// {"account": id} chooses one.
func (s *Server) handlePxiHarnessAccounts(w http.ResponseWriter, r *http.Request) {
	list := pxiHarnessAccounts()
	if r.Method == http.MethodPost {
		var b struct {
			Account string `json:"account"`
		}
		if !pxiReadBody(w, r, &b) {
			return
		}
		if _, ok := pxiHarnessSelected(list, b.Account); !ok || !pxiHarnessKnown(list, b.Account) {
			fail(w, http.StatusBadRequest, "not a logged-in Claude account: "+b.Account)
			return
		}
		pxiHarnessFileMu.Lock()
		c := pxiHarnessRead()
		c.Account = b.Account
		pxiHarnessWrite(c)
		pxiHarnessFileMu.Unlock()
	}
	pxiHarnessFileMu.Lock()
	sel, _ := pxiHarnessSelected(list, pxiHarnessRead().Account)
	pxiHarnessFileMu.Unlock()
	writeJSON(w, map[string]any{"accounts": list, "selected": sel.ID})
}

func pxiHarnessKnown(list []pxiHarnessAccount, id string) bool {
	for _, a := range list {
		if a.ID == id {
			return true
		}
	}
	return false
}
