//go:build !windows

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSSH puts an `ssh` on PATH that runs the remote command locally with sh,
// so the tests exercise the real quoting without a server.
func fakeSSH(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift; shift\nexec sh -c \"$1\"\n"
	os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestPxiSSHRemoteBrowse(t *testing.T) {
	fakeSSH(t)
	remote := t.TempDir()
	if r, err := filepath.EvalSymlinks(remote); err == nil {
		remote = r
	}
	os.MkdirAll(filepath.Join(remote, "src", "it's here"), 0o755)
	os.WriteFile(filepath.Join(remote, "src", "it's here", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(remote, ".env"), []byte("A=1\n"), 0o644)
	os.WriteFile(filepath.Join(remote, "blob.bin"), []byte{0, 1, 2}, 0o644)
	os.MkdirAll(filepath.Join(remote, ".git"), 0o755)

	s, _ := newTestServer(t)
	h := hostGuard(s)
	code, m := sshPost(h, "/api/pxi/ssh/remotes", `{"host":"devbox","path":`+jsonStr(remote)+`}`)
	if code != 200 {
		t.Fatalf("connect: %d %v", code, m)
	}
	rem := m["remote"].(map[string]any)
	id := rem["id"].(string)
	if rem["path"] != remote {
		t.Fatalf("path %v", rem["path"])
	}
	list := func(dir string) []string {
		rec := sshServe(h, "GET", "/api/pxi/ssh/list?"+url.Values{"id": {id}, "dir": {dir}}.Encode(), "", "")
		var j struct{ Entries []pxiRemoteEntry }
		json.Unmarshal(rec.Body.Bytes(), &j)
		var out []string
		for _, e := range j.Entries {
			n := e.Path
			if e.Dir {
				n += "/"
			}
			out = append(out, n)
		}
		return out
	}
	if got := strings.Join(list(""), ","); got != "src/,.env,blob.bin" {
		t.Errorf("root: %s", got)
	}
	if got := strings.Join(list("src"), ","); got != "src/it's here/" {
		t.Errorf("src: %s", got)
	}
	file := func(p string) (int, map[string]any) {
		rec := sshServe(h, "GET", "/api/file?"+url.Values{"path": {p}}.Encode(), "", "")
		var j map[string]any
		json.Unmarshal(rec.Body.Bytes(), &j)
		return rec.Code, j
	}
	code, j := file("@ssh/" + id + "/src/it's here/main.go")
	if code != 200 || j["total"].(float64) != 4 || j["lang"] != "Go" {
		t.Fatalf("file: %d %v", code, j)
	}
	if code, _ := file("@ssh/" + id + "/blob.bin"); code != 415 {
		t.Errorf("binary: %d", code)
	}
	if code, _ := file("@ssh/" + id + "/../../etc/passwd"); code != 400 {
		t.Errorf("escape: %d", code)
	}
	if rec := sshServe(h, "GET", "/api/pxi/ssh/list?"+url.Values{"id": {id}, "dir": {"../.."}}.Encode(), "", ""); rec.Code != 400 {
		t.Errorf("list escape: %d", rec.Code)
	}
	if code, _ := sshPost(h, "/api/pxi/ssh/remotes", `{"host":"-oProxyCommand=x","path":"/"}`); code == 200 {
		t.Error("option as host accepted")
	}
	if rec := sshServe(h, "POST", "/api/pxi/ssh/remotes", "http://evil.example", `{"host":"devbox","path":"/"}`); rec.Code == 200 {
		t.Error("cross-site connect accepted")
	}
	if code, _ := sshPost(h, "/api/pxi/ssh/remotes", `{"remove":"`+id+`"}`); code != 200 || len(pxiRemotesRead()) != 0 {
		t.Error("remove failed")
	}
}

// sshServe is serve() from this machine (the remote routes are loopback only).
func sshServe(h http.Handler, method, target, origin, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Host, r.RemoteAddr = "127.0.0.1:7777", "127.0.0.1:50000"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func sshPost(h http.Handler, path, body string) (int, map[string]any) {
	rec := sshServe(h, "POST", path, "http://127.0.0.1:7777", body)
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }
