package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guardedServer(t *testing.T) (http.Handler, *Server, string) {
	t.Helper()
	s, root := newTestServer(t)
	return hostGuard(s), s, root
}

func serve(h http.Handler, method, target, host, origin, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestHostGuardRefusesDNSRebinding(t *testing.T) {
	h, _, _ := guardedServer(t)
	for _, host := range []string{"evil.example:7777", "attacker.com", "localhost.evil.com:7777"} {
		if rec := serve(h, "GET", "/api/file?path=main.go", host, "", ""); rec.Code != http.StatusForbidden {
			t.Errorf("Host %s: want 403, got %d", host, rec.Code)
		}
	}
	for _, host := range []string{"localhost:7777", "LOCALHOST:7777", "127.0.0.1:7777", "[::1]:7777", "192.168.1.20:7777"} {
		if rec := serve(h, "GET", "/api/file?path=main.go", host, "", ""); rec.Code != http.StatusOK {
			t.Errorf("Host %s: want 200, got %d %s", host, rec.Code, rec.Body.String())
		}
	}
}

func TestHostGuardRefusesCrossSiteWrites(t *testing.T) {
	h, _, root := guardedServer(t)
	target := filepath.Join(root, "main.go")
	before, _ := os.ReadFile(target)
	body := `{"path":"main.go","content":"pwned\n"}`
	host := "127.0.0.1:7777"

	for _, origin := range []string{"http://evil.example", "null", "http://127.0.0.1:9999"} {
		if rec := serve(h, "POST", "/api/file/save", host, origin, body); rec.Code != http.StatusForbidden {
			t.Errorf("save from %s: want 403, got %d", origin, rec.Code)
		}
		if rec := serve(h, "POST", "/api/fs/new", host, origin, `{"path":".git/hooks/pre-commit","type":"file"}`); rec.Code != http.StatusForbidden {
			t.Errorf("fs/new from %s: want 403, got %d", origin, rec.Code)
		}
	}
	if after, _ := os.ReadFile(target); string(after) != string(before) {
		t.Fatalf("cross-site save changed the file: %q", after)
	}
	r := httptest.NewRequest("POST", "/api/file/save", strings.NewReader(body))
	r.Host = host
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Sec-Fetch-Site cross-site: want 403, got %d", rec.Code)
	}

	// px0's own page, and non-browser clients without Origin, still save.
	if rec := serve(h, "POST", "/api/file/save", host, "http://127.0.0.1:7777", body); rec.Code != http.StatusOK {
		t.Fatalf("same-origin save: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(h, "POST", "/api/file/save", host, "", `{"path":"main.go","content":"ok\n"}`); rec.Code != http.StatusOK {
		t.Fatalf("no-Origin save: want 200, got %d", rec.Code)
	}
}

func TestSafePathRefusesSymlinkEscape(t *testing.T) {
	s, root := newTestServer(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("must stay outside\n"), 0o600)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink(outside, filepath.Join(root, "escape")))                                   // dir link out
	must(os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "leak.txt")))    // file link out
	must(os.Symlink(filepath.Join(outside, "missing.sh"), filepath.Join(root, "dangling.sh"))) // dangling, out
	must(os.Symlink(filepath.Join(root, "main.go"), filepath.Join(root, "alias.go")))          // stays inside

	for _, p := range []string{"escape/secret.txt", "leak.txt", "escape/new-file.txt", "dangling.sh"} {
		if _, _, ok := s.safePath(p); ok {
			t.Errorf("%s: safePath allowed a path outside the workspace", p)
		}
	}
	for _, p := range []string{"main.go", "alias.go", "sub/deep.py", "not-yet-created.go", "newdir/new.go", "."} {
		if _, _, ok := s.safePath(p); !ok {
			t.Errorf("%s: safePath refused a path inside the workspace", p)
		}
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/raw?path=escape%2Fsecret.txt", nil))
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "must stay outside") {
		t.Fatalf("raw endpoint served a file outside the workspace: %d", rec.Code)
	}
	// A save through a dangling link must not create the target outside.
	rec = serve(hostGuard(s), "POST", "/api/file/save", "127.0.0.1:7777", "", `{"path":"dangling.sh","content":"echo hi\n"}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("save through dangling symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "missing.sh")); err == nil {
		t.Fatal("save created a file outside the workspace")
	}
}
