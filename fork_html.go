package main

// pxi HTML preview: serves a workspace's HTML file (and the CSS, images and
// fonts it links to) for web/src/pxi-html.js to show in an iframe.
//
// /api/raw deliberately serves workspace files as downloads, never as
// documents: an HTML file rendered on pxi's own origin could read pxi's API.
// This endpoint renders HTML, so every response carries a CSP "sandbox"
// directive without allow-same-origin: the document gets an opaque origin, so
// it cannot read pxi's API or storage, and its POSTs (Origin "null") are refused
// by hostGuard. Scripts, plugins, forms, network requests and anything from
// outside the workspace are blocked. Paths are part of the URL, so relative
// links in the HTML resolve to sibling files through this same endpoint and the
// same sandbox (safePath / worktree rules, no symlink escapes).

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const pxiHTMLPrefix = "/api/pxi/html/"

// The preview's policy: sandboxed (opaque origin, no scripts), may be framed
// only by pxi's own page, may load only workspace files and data: URLs.
const pxiHTMLPolicy = "sandbox; default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; " +
	"font-src 'self' data:; media-src 'self' data: blob:; script-src 'none'; connect-src 'none'; frame-src 'none'; " +
	"object-src 'none'; form-action 'none'; base-uri 'self'; frame-ancestors 'self'"

// Types the preview serves; anything else is refused rather than guessed.
var pxiHTMLTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".htm": "text/html; charset=utf-8",
	".css": "text/css; charset=utf-8",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".ico": "image/x-icon", ".bmp": "image/bmp",
	".svg":  "image/svg+xml",
	".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf",
	".mp4": "video/mp4", ".webm": "video/webm", ".mp3": "audio/mpeg", ".wav": "audio/wav", ".ogg": "audio/ogg",
}

func (s *Server) registerPxiHTMLRoutes() {
	s.mux.HandleFunc(s.routePath(pxiHTMLPrefix), s.handlePxiHTML)
}

func (s *Server) handlePxiHTML(w http.ResponseWriter, r *http.Request) {
	// Replace the page's own policy (which forbids framing) before anything
	// else, so even an error response is sandboxed.
	w.Header().Set("Content-Security-Policy", pxiHTMLPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, s.routePath(pxiHTMLPrefix))
	abs, _, ok := s.resolvePath(rel)
	if !ok || rel == "" || filepath.IsAbs(filepath.FromSlash(rel)) {
		fail(w, http.StatusBadRequest, "bad path")
		return
	}
	ext := strings.ToLower(filepath.Ext(abs))
	ct, known := pxiHTMLTypes[ext]
	if !known {
		if t := mime.TypeByExtension(ext); strings.HasPrefix(t, "image/") || strings.HasPrefix(t, "font/") {
			ct, known = t, true
		}
	}
	if !known {
		fail(w, http.StatusUnsupportedMediaType, "not served in the preview")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, "", st.ModTime(), f)
}
