//go:build !windows

package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPxiTerminalRunsAShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir()) // opening a terminal starts the IDE bridge, which writes a lock file
	s, root := newTestServer(t)
	srv := httptest.NewServer(hostGuard(s))
	defer srv.Close()
	post := func(path, body string) (int, map[string]any) {
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
		return res.StatusCode, m
	}

	code, term := post("/api/pxi/term/open", `{"cols":80,"rows":24}`)
	if code != 200 || term["alive"] != true || term["cwd"] != root {
		t.Fatalf("open: %d %v", code, term)
	}
	id := int(term["id"].(float64))

	res, err := http.Get(srv.URL + "/api/pxi/term/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var ev pxiTermEvent
			if json.Unmarshal([]byte(line), &ev) == nil && ev.ID == id && ev.Data != "" {
				b, _ := base64.StdEncoding.DecodeString(ev.Data)
				out <- string(b)
			}
		}
		close(out)
	}()

	if code, _ := post("/api/pxi/term/input", `{"id":`+strconv.Itoa(id)+`,"data":"echo pxi-$((40+2))\n"}`); code != 200 {
		t.Fatalf("input: %d", code)
	}
	var seen strings.Builder
	deadline := time.After(5 * time.Second)
	for !strings.Contains(seen.String(), "pxi-42") {
		select {
		case chunk, ok := <-out:
			if !ok {
				t.Fatalf("stream ended; got %q", seen.String())
			}
			seen.WriteString(chunk)
		case <-deadline:
			t.Fatalf("no output from the shell; got %q", seen.String())
		}
	}

	if code, _ := post("/api/pxi/term/resize", `{"id":`+strconv.Itoa(id)+`,"cols":120,"rows":40}`); code != 200 {
		t.Errorf("resize: %d", code)
	}
	if code, _ := post("/api/pxi/term/close", `{"id":`+strconv.Itoa(id)+`}`); code != 200 {
		t.Errorf("close: %d", code)
	}
	r2, _ := http.Get(srv.URL + "/api/pxi/term/list")
	body, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if strings.Contains(string(body), `"id":`+strconv.Itoa(id)+`,`) {
		t.Errorf("closed terminal still listed: %s", body)
	}
}

func TestPxiTerminalRefusesOtherMachines(t *testing.T) {
	s, _ := newTestServer(t)
	h := hostGuard(s)
	// A page reached over the network (Host is the machine's LAN address).
	rec := serve(h, "POST", "/api/pxi/term/open", "192.168.1.20:7777", "http://192.168.1.20:7777", `{}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("LAN host: want 403, got %d", rec.Code)
	}
	// A client on another machine, whatever Host it claims.
	r := httptest.NewRequest("GET", "/api/pxi/term/list", nil)
	r.Host = "127.0.0.1:7777"
	r.RemoteAddr = "192.168.1.30:51000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("remote client: want 403, got %d", w.Code)
	}
	// Another website, even on this machine.
	rec = serve(h, "POST", "/api/pxi/term/open", "127.0.0.1:7777", "http://evil.example", `{}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site: want 403, got %d", rec.Code)
	}
}
