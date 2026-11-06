package main

// Security hardening carried by the pxi fork until upstream px0 ships its own
// fixes. Kept in one file, with one-line hooks in main.go and server.go, so an
// upstream merge stays easy and this file can be deleted once upstream covers it.
//
//   - DNS rebinding (upstream issue #241, ported from PR #244 by
//     chakrabortyShayak): only "localhost" or an IP literal is accepted as the
//     request Host. A page on an attacker's domain can point that domain at
//     127.0.0.1, but its requests then carry the attacker's hostname and are
//     refused, so it cannot read files, settings or tokens through GET endpoints.
//   - Symlink escape (upstream issue #186, based on PR #187 by Abhishek Kumar):
//     a path inside the workspace must still be inside it after symlinks are
//     resolved. Unlike PR #187 this also works for paths that do not exist yet
//     (deleted files shown in a diff, new files being created), and refuses a
//     dangling symlink, whose target a write would otherwise create outside.

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// allowedRequestHost accepts only names whose address DNS cannot change.
func allowedRequestHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil
}

// hostGuard refuses every request whose Host is a DNS name other than localhost.
// A reverse proxy in front of px0 must send Host as localhost:<port> or an IP.
//
// It also refuses a state-changing request (anything but GET, HEAD, OPTIONS)
// that a browser sent from another origin. Most POST handlers check this
// themselves (localPost), but /api/file/save and /api/fs/new did not, so any
// website open in the same browser could write files into the workspace, for
// example a .git/hooks script that runs on the next commit. Requests without an
// Origin header (curl, scripts) are not from a browser page and pass.
func hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedRequestHost(r.Host) {
			w.Header().Set("Cache-Control", "no-store")
			fail(w, http.StatusForbidden, "open pxi by IP address or localhost")
			return
		}
		if !sameOriginWrite(r) {
			w.Header().Set("Cache-Control", "no-store")
			fail(w, http.StatusForbidden, "request did not come from pxi")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginWrite reports whether a request may change state: safe methods
// always may; others only when no browser origin is named or it is px0's own.
func sameOriginWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	o, err := url.Parse(origin)
	return err == nil && o.Host != "" && o.Host == r.Host
}

var errDanglingLink = errors.New("dangling symlink")

// realPath resolves symlinks in p. For a path that does not exist, it resolves
// the deepest existing ancestor and appends the rest unchanged.
func realPath(p string) (string, error) {
	rest := ""
	for {
		r, err := filepath.EvalSymlinks(p)
		if err == nil {
			if rest == "" {
				return r, nil
			}
			return filepath.Join(r, rest), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if _, lerr := os.Lstat(p); lerr == nil {
			return "", errDanglingLink // p exists but points nowhere
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		if rest == "" {
			rest = filepath.Base(p)
		} else {
			rest = filepath.Join(filepath.Base(p), rest)
		}
		p = parent
	}
}

// withinReal reports whether abs stays inside root once symlinks are resolved.
func withinReal(root, abs string) bool {
	rr, err := realPath(root)
	if err != nil {
		return false
	}
	ra, err := realPath(abs)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rr, ra)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
