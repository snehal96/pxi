package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func htmlGet(h http.Handler, target, host string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	r.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPxiHTMLPreviewIsSandboxed(t *testing.T) {
	s, root := newTestServer(t)
	os.MkdirAll(filepath.Join(root, "site"), 0o755)
	os.WriteFile(filepath.Join(root, "site", "index.html"), []byte(`<link rel="stylesheet" href="style.css"><h1>Hi</h1>`), 0o644)
	os.WriteFile(filepath.Join(root, "site", "style.css"), []byte(`h1{color:red}`), 0o644)
	os.WriteFile(filepath.Join(root, "site", "app.js"), []byte(`alert(1)`), 0o644)
	h := hostGuard(s)
	host := "127.0.0.1:7777"

	w := htmlGet(h, "/api/pxi/html/site/index.html", host)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>Hi</h1>") {
		t.Fatalf("index: %d %q", w.Code, w.Body.String())
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox", "script-src 'none'", "connect-src 'none'", "frame-ancestors 'self'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "allow-same-origin") || strings.Contains(csp, "allow-scripts") {
		t.Errorf("CSP sandbox must not allow same-origin or scripts: %s", csp)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content type %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Disposition") != "" {
		t.Errorf("headers: %v", w.Header())
	}

	// Siblings the page links to resolve through the same endpoint.
	if w := htmlGet(h, "/api/pxi/html/site/style.css", host); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
		t.Errorf("css: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	// Scripts are not served at all (and could not run anyway).
	if w := htmlGet(h, "/api/pxi/html/site/app.js", host); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("js: want 415, got %d", w.Code)
	}
	// The rest of pxi keeps its own policy (no framing).
	if w := htmlGet(h, "/api/meta", host); strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("pxi's own pages must keep their policy")
	}
}

func TestPxiHTMLPreviewStaysInTheWorkspace(t *testing.T) {
	s, root := newTestServer(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.html"), []byte("<p>secret</p>"), 0o644)
	os.Symlink(outside, filepath.Join(root, "out"))
	h := hostGuard(s)
	for _, p := range []string{
		"/api/pxi/html/../secret.html",
		"/api/pxi/html/out/secret.html",
		"/api/pxi/html//etc/hosts.html",
		"/api/pxi/html/%2e%2e/secret.html",
		"/api/pxi/html/",
	} {
		w := htmlGet(h, p, "127.0.0.1:7777")
		// ServeMux answers "../" with a redirect to the cleaned path, which is
		// outside this endpoint; what matters is that nothing is served.
		if w.Code == 200 || strings.Contains(w.Body.String(), "<p>secret</p>") {
			t.Errorf("%s: served (%d) %q", p, w.Code, w.Body.String())
		}
	}
	// A rebound DNS name is refused like every other request.
	os.WriteFile(filepath.Join(root, "page.html"), []byte("<p>ok</p>"), 0o644)
	if w := htmlGet(h, "/api/pxi/html/page.html", "evil.example:7777"); w.Code != http.StatusForbidden {
		t.Errorf("DNS host: want 403, got %d", w.Code)
	}
	if w := htmlGet(h, "/api/pxi/html/page.html", "127.0.0.1:7777"); w.Code != 200 {
		t.Errorf("page: %d", w.Code)
	}
}

func TestPxiHTMLPreviewServesWorktrees(t *testing.T) {
	root, a, _ := wtRepo(t)
	os.WriteFile(filepath.Join(a, "doc.html"), []byte("<p>from wt-a</p>"), 0o644)
	s := wtServer(t, root)
	w := htmlGet(hostGuard(s), "/api/pxi/html/@wt/wt-a/doc.html", "127.0.0.1:7777")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from wt-a") {
		t.Fatalf("worktree page: %d %q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("worktree page not sandboxed")
	}
}
